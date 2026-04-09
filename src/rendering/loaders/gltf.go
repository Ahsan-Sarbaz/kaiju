/******************************************************************************/
/* gltf.go                                                                    */
/******************************************************************************/
/*                            This file is part of                            */
/*                                KAIJU ENGINE                                */
/*                          https://kaijuengine.com/                          */
/******************************************************************************/
/* MIT License                                                                */
/*                                                                            */
/* Copyright (c) 2023-present Kaiju Engine authors (AUTHORS.md).              */
/* Copyright (c) 2015-present Brent Farris.                                   */
/*                                                                            */
/* May all those that this source may reach be blessed by the LORD and find   */
/* peace and joy in life.                                                     */
/* Everyone who drinks of this water will be thirsty again; but whoever       */
/* drinks of the water that I will give him shall never thirst; John 4:13-14  */
/*                                                                            */
/* Permission is hereby granted, free of charge, to any person obtaining a    */
/* copy of this software and associated documentation files (the "Software"), */
/* to deal in the Software without restriction, including without limitation  */
/* the rights to use, copy, modify, merge, publish, distribute, sublicense,   */
/* and/or sell copies of the Software, and to permit persons to whom the      */
/* Software is furnished to do so, subject to the following conditions:       */
/*                                                                            */
/* The above copyright notice and this permission notice shall be included in */
/* all copies or substantial portions of the Software.                        */
/*                                                                            */
/* THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS    */
/* OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF                 */
/* MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.     */
/* IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY       */
/* CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT  */
/* OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE      */
/* OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.                              */
/******************************************************************************/

package loaders

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"kaijuengine.com/engine/assets"
	"kaijuengine.com/matrix"
	"kaijuengine.com/platform/profiler/tracing"
	"kaijuengine.com/rendering"
	"kaijuengine.com/rendering/loaders/gltf"
	"kaijuengine.com/rendering/loaders/load_result"
)

type fullGLTF struct {
	path string
	glTF gltf.GLTF
	bins [][]byte
}

type rawMeshData struct {
	verts   []rendering.Vertex
	indices []uint32
}

func decodeDataURI(uri string) ([]byte, error) {
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, errors.New("invalid data uri")
	}
	header := uri[:comma]
	payload := uri[comma+1:]
	if strings.HasSuffix(strings.ToLower(header), ";base64") || strings.Contains(strings.ToLower(header), ";base64;") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			return decoded, nil
		}
		return base64.RawStdEncoding.DecodeString(payload)
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, err
	}
	return []byte(decoded), nil
}

func readExternalOrDataURI(root, uri string, assetDB assets.Database) ([]byte, error) {
	if uri == "" {
		return nil, errors.New("empty uri")
	}
	if strings.HasPrefix(strings.ToLower(uri), "data:") {
		return decodeDataURI(uri)
	}
	path := filepath.Join(root, filepath.FromSlash(uri))
	return assetDB.Read(path)
}

func readFileGLB(file string, assetDB assets.Database) (fullGLTF, error) {
	defer tracing.NewRegion("loaders.readFileGLB").End()

	const headerSize = 12
	const chunkHeaderSize = 8

	g := fullGLTF{path: file}
	data, err := assetDB.Read(file)
	if err != nil {
		return g, err
	}
	if len(data) < headerSize {
		return g, errors.New("invalid glb file")
	}
	if string(data[:4]) != "glTF" {
		return g, errors.New("invalid glb file")
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 2 {
		return g, fmt.Errorf("unsupported glb version: %d", version)
	}
	declaredLength := binary.LittleEndian.Uint32(data[8:12])
	if int(declaredLength) != len(data) {
		return g, errors.New("invalid glb file length")
	}

	var jsonChunk []byte
	var binChunk []byte
	cursor := headerSize
	for cursor < len(data) {
		if len(data[cursor:]) < chunkHeaderSize {
			return g, errors.New("invalid glb chunk header")
		}
		chunkLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		chunkType := string(data[cursor+4 : cursor+8])
		cursor += chunkHeaderSize
		if chunkLen < 0 || cursor+chunkLen > len(data) {
			return g, errors.New("invalid glb chunk size")
		}
		chunkData := data[cursor : cursor+chunkLen]
		cursor += chunkLen
		switch chunkType {
		case "JSON":
			if jsonChunk == nil {
				jsonChunk = chunkData
			}
		case "BIN\x00":
			if binChunk == nil {
				binChunk = chunkData
			}
		}
	}
	if len(jsonChunk) == 0 {
		return g, errors.New("glb missing json chunk")
	}

	g.glTF, err = gltf.LoadGLTF(string(jsonChunk))
	if err != nil {
		return g, err
	}
	g.glTF.Asset.FilePath = file
	g.bins = make([][]byte, len(g.glTF.Buffers))
	root := filepath.Dir(file)
	for i, buffer := range g.glTF.Buffers {
		switch {
		case buffer.URI == "" && i == 0:
			g.bins[i] = binChunk
		case buffer.URI == "":
			if buffer.ByteLength != 0 {
				return g, fmt.Errorf("buffer %d has no uri and no glb bin chunk", i)
			}
		case strings.HasPrefix(strings.ToLower(buffer.URI), "data:"):
			g.bins[i], err = decodeDataURI(buffer.URI)
			if err != nil {
				return g, err
			}
		default:
			g.bins[i], err = readExternalOrDataURI(root, buffer.URI, assetDB)
			if err != nil {
				return g, err
			}
		}
		if int(buffer.ByteLength) > len(g.bins[i]) {
			return g, fmt.Errorf("buffer %d shorter than declared byteLength", i)
		}
	}
	return g, nil
}

func readFileGLTF(file string, assetDB assets.Database) (fullGLTF, error) {
	defer tracing.NewRegion("loaders.readFileGLTF").End()
	g := fullGLTF{path: file}
	str, err := assetDB.ReadText(file)
	if err != nil {
		return g, err
	}
	g.glTF, err = gltf.LoadGLTF(str)
	if err != nil {
		return g, err
	}
	g.glTF.Asset.FilePath = file
	g.bins = make([][]byte, len(g.glTF.Buffers))
	root := filepath.Dir(file)
	for i, buffer := range g.glTF.Buffers {
		if buffer.URI == "" {
			if buffer.ByteLength != 0 {
				return g, fmt.Errorf("buffer %d is missing uri", i)
			}
			continue
		}
		g.bins[i], err = readExternalOrDataURI(root, buffer.URI, assetDB)
		if err != nil {
			return g, err
		}
		if int(buffer.ByteLength) > len(g.bins[i]) {
			return g, fmt.Errorf("buffer %d shorter than declared byteLength", i)
		}
	}
	return g, nil
}

func GLTF(path string, assetDB assets.Database) (load_result.Result, error) {
	defer tracing.NewRegion("loaders.GLTF").End()
	if !assetDB.Exists(path) {
		return load_result.Result{}, errors.New("file does not exist")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".glb":
		g, err := readFileGLB(path, assetDB)
		if err != nil {
			return load_result.Result{}, err
		}
		return gltfParse(&g)
	case ".gltf":
		g, err := readFileGLTF(path, assetDB)
		if err != nil {
			return load_result.Result{}, err
		}
		return gltfParse(&g)
	default:
		return load_result.Result{}, errors.New("invalid file extension")
	}
}

func gltfParse(doc *fullGLTF) (load_result.Result, error) {
	defer tracing.NewRegion("loaders.gltfParse").End()
	if doc.glTF.Asset.Version != "2.0" {
		return load_result.Result{}, fmt.Errorf("unsupported glTF version %q, expected 2.0", doc.glTF.Asset.Version)
	}
	res := load_result.Result{}
	res.Nodes = make([]load_result.Node, len(doc.glTF.Nodes))
	for i := range res.Nodes {
		res.Nodes[i].Parent = -1
		res.Nodes[i].Attributes = make(map[string]any)
		res.Nodes[i].Scale = matrix.Vec3One()
		res.Nodes[i].Rotation = matrix.QuaternionIdentity()
	}

	// TODO: Deal with multiple skins.
	if len(doc.glTF.Skins) > 0 {
		skin := &doc.glTF.Skins[0]
		bindMats := make([]matrix.Mat4, 0, len(skin.Joints))
		if skin.InverseBindMatrices != nil {
			acc, err := gltfAccessorByIntIndex(doc, *skin.InverseBindMatrices)
			if err != nil {
				return res, err
			}
			if acc.ComponentType != gltf.FLOAT || acc.Type != gltf.MAT4 {
				return res, errors.New("inverse bind matrices accessor must be float MAT4")
			}
			floats, err := gltfReadAccessorFloats(doc, acc)
			if err != nil {
				return res, err
			}
			for i := 0; i+15 < len(floats); i += 16 {
				bindMats = append(bindMats, matrix.Mat4FromSlice([]matrix.Float{
					matrix.Float(floats[i+0]), matrix.Float(floats[i+1]), matrix.Float(floats[i+2]), matrix.Float(floats[i+3]),
					matrix.Float(floats[i+4]), matrix.Float(floats[i+5]), matrix.Float(floats[i+6]), matrix.Float(floats[i+7]),
					matrix.Float(floats[i+8]), matrix.Float(floats[i+9]), matrix.Float(floats[i+10]), matrix.Float(floats[i+11]),
					matrix.Float(floats[i+12]), matrix.Float(floats[i+13]), matrix.Float(floats[i+14]), matrix.Float(floats[i+15]),
				}))
			}
		}
		for i, id := range skin.Joints {
			if id < 0 || int(id) >= len(doc.glTF.Nodes) {
				return res, fmt.Errorf("invalid joint node index %d", id)
			}
			if strings.HasPrefix(doc.glTF.Nodes[id].Name, "DRV_") || strings.HasPrefix(doc.glTF.Nodes[id].Name, "CTRL_") {
				continue
			}
			jointMat := matrix.Mat4Identity()
			if i < len(bindMats) {
				jointMat = bindMats[i]
			}
			res.Joints = append(res.Joints, load_result.Joint{Id: id, Skin: jointMat})
		}
	}

	for i := range doc.glTF.Nodes {
		n := &doc.glTF.Nodes[i]
		res.Nodes[i].Id = int32(i)
		res.Nodes[i].Name = n.Name
		if n.Extras != nil {
			res.Nodes[i].Attributes = n.Extras
		}
		for _, childID := range n.Children {
			if childID < 0 || int(childID) >= len(res.Nodes) {
				return res, fmt.Errorf("invalid child node index %d", childID)
			}
			res.Nodes[childID].Parent = i
		}
		if n.Matrix != nil {
			res.Nodes[i].Position = n.Matrix.ExtractPosition()
			res.Nodes[i].Scale = n.Matrix.ExtractScale()
			res.Nodes[i].Rotation = n.Matrix.ExtractRotation()
		} else {
			if n.Scale != nil {
				res.Nodes[i].Scale = *n.Scale
			}
			if n.Rotation != nil {
				res.Nodes[i].Rotation = matrix.QuaternionFromXYZW(*n.Rotation)
			}
			if n.Translation != nil {
				res.Nodes[i].Position = *n.Translation
			}
		}
		if n.Mesh == nil {
			continue
		}
		if *n.Mesh < 0 || int(*n.Mesh) >= len(doc.glTF.Meshes) {
			return res, fmt.Errorf("invalid mesh index %d", *n.Mesh)
		}
		m := &doc.glTF.Meshes[*n.Mesh]
		for p := range m.Primitives {
			prim := &m.Primitives[p]
			if prim.Mode != 0 && prim.Mode != 4 {
				continue
			}
			rmd := new(rawMeshData)
			verts, err := gltfReadMeshVerts(m, doc, p)
			if err != nil {
				return res, err
			}
			indices, err := gltfReadMeshIndices(m, doc, p, len(verts))
			if err != nil {
				return res, err
			}
			rmd.verts = verts
			rmd.indices = indices
			textures := gltfReadMeshTextures(m, &doc.glTF, p)
			key := fmt.Sprintf("%s/%s", doc.path, m.Name)
			if p > 0 {
				key += fmt.Sprintf("_%d", p+1)
			}
			res.Add(n.Name, key, rmd.verts, rmd.indices, textures, &res.Nodes[i])
		}
	}

	res.Animations = gltfReadAnimations(doc)
	for i := range doc.glTF.Animations {
		for j := range doc.glTF.Animations[i].Channels {
			nid := doc.glTF.Animations[i].Channels[j].Target.Node
			if nid < 0 || int(nid) >= len(res.Nodes) {
				continue
			}
			res.Nodes[nid].IsAnimated = true
			p := res.Nodes[nid].Parent
			for p >= 0 {
				res.Nodes[p].IsAnimated = true
				p = res.Nodes[p].Parent
			}
		}
	}
	return res, nil
}

func gltfAttr(primitive gltf.Primitive, cmp string) (uint32, bool) {
	defer tracing.NewRegion("loaders.gltfAttr").End()
	idx, ok := primitive.Attributes[cmp]
	return idx, ok
}

func gltfAccessorByIntIndex(doc *fullGLTF, idx int32) (*gltf.Accessor, error) {
	if idx < 0 || int(idx) >= len(doc.glTF.Accessors) {
		return nil, fmt.Errorf("invalid accessor index %d", idx)
	}
	return &doc.glTF.Accessors[idx], nil
}

func gltfAccessorByUintIndex(doc *fullGLTF, idx uint32) (*gltf.Accessor, error) {
	if int(idx) >= len(doc.glTF.Accessors) {
		return nil, fmt.Errorf("invalid accessor index %d", idx)
	}
	return &doc.glTF.Accessors[idx], nil
}

func gltfAccessorComponentCount(acc *gltf.Accessor) (int, error) {
	switch acc.Type {
	case gltf.SCALAR:
		return 1, nil
	case gltf.VEC2:
		return 2, nil
	case gltf.VEC3:
		return 3, nil
	case gltf.VEC4, gltf.MAT2:
		return 4, nil
	case gltf.MAT3:
		return 9, nil
	case gltf.MAT4:
		return 16, nil
	default:
		return 0, fmt.Errorf("unsupported accessor type %q", acc.Type)
	}
}

func gltfComponentByteSize(componentType gltf.ComponentType) (int, error) {
	switch componentType {
	case gltf.BYTE, gltf.UNSIGNED_BYTE:
		return 1, nil
	case gltf.SHORT, gltf.UNSIGNED_SHORT:
		return 2, nil
	case gltf.UNSIGNED_INT, gltf.FLOAT:
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported component type %d", componentType)
	}
}

func gltfAccessorLayout(doc *fullGLTF, acc *gltf.Accessor) ([]byte, int, int, int, error) {
	if acc == nil {
		return nil, 0, 0, 0, errors.New("nil accessor")
	}
	if acc.Count < 0 {
		return nil, 0, 0, 0, errors.New("invalid accessor count")
	}
	compCount, err := gltfAccessorComponentCount(acc)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	compSize, err := gltfComponentByteSize(acc.ComponentType)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	elemSize := compCount * compSize
	count := int(acc.Count)

	// Read base data from bufferView (or start with zeroes if absent).
	var base []byte
	stride := elemSize
	if acc.BufferView != nil {
		viewIdx := *acc.BufferView
		if viewIdx < 0 || int(viewIdx) >= len(doc.glTF.BufferViews) {
			return nil, 0, 0, 0, fmt.Errorf("invalid buffer view index %d", viewIdx)
		}
		view := &doc.glTF.BufferViews[viewIdx]
		if view.Buffer < 0 || int(view.Buffer) >= len(doc.bins) {
			return nil, 0, 0, 0, fmt.Errorf("invalid buffer index %d", view.Buffer)
		}
		buffer := doc.bins[view.Buffer]
		bvStride := int(view.ByteStride)
		if bvStride == 0 {
			bvStride = elemSize
		}
		if bvStride < elemSize {
			return nil, 0, 0, 0, errors.New("buffer view stride smaller than accessor element size")
		}
		start := int(view.ByteOffset + acc.ByteOffset)
		viewEnd := int(view.ByteOffset + view.ByteLength)
		if start < 0 || start > len(buffer) || viewEnd < start || viewEnd > len(buffer) {
			return nil, 0, 0, 0, errors.New("invalid accessor byte range")
		}
		if count == 0 {
			if acc.Sparse == nil {
				return buffer[start:start], elemSize, bvStride, 0, nil
			}
			return make([]byte, 0), elemSize, elemSize, 0, nil
		}
		end := start + (count-1)*bvStride + elemSize
		if end > viewEnd || end > len(buffer) {
			return nil, 0, 0, 0, errors.New("accessor exceeds buffer view")
		}
		if acc.Sparse == nil {
			return buffer[start:end], elemSize, bvStride, count, nil
		}
		// Need a flat copy to apply sparse overrides.
		base = make([]byte, count*elemSize)
		for i := 0; i < count; i++ {
			copy(base[i*elemSize:], buffer[start+i*bvStride:start+i*bvStride+elemSize])
		}
		stride = elemSize
	} else if acc.Sparse == nil {
		return nil, 0, 0, 0, errors.New("accessor has no bufferView and no sparse data")
	} else {
		base = make([]byte, count*elemSize)
	}

	// Apply sparse overrides.
	sp := acc.Sparse
	if sp.Count <= 0 {
		return base, elemSize, stride, count, nil
	}
	// Read sparse indices.
	idxSize, err := gltfComponentByteSize(sp.Indices.ComponentType)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("sparse indices: %w", err)
	}
	idxBVIdx := sp.Indices.BufferView
	if idxBVIdx < 0 || int(idxBVIdx) >= len(doc.glTF.BufferViews) {
		return nil, 0, 0, 0, fmt.Errorf("sparse indices: invalid buffer view index %d", idxBVIdx)
	}
	idxBV := &doc.glTF.BufferViews[idxBVIdx]
	if idxBV.Buffer < 0 || int(idxBV.Buffer) >= len(doc.bins) {
		return nil, 0, 0, 0, fmt.Errorf("sparse indices: invalid buffer index %d", idxBV.Buffer)
	}
	idxBuf := doc.bins[idxBV.Buffer]
	idxStart := int(idxBV.ByteOffset + sp.Indices.ByteOffset)
	idxEnd := idxStart + int(sp.Count)*idxSize
	if idxStart < 0 || idxEnd > len(idxBuf) {
		return nil, 0, 0, 0, errors.New("sparse indices out of buffer bounds")
	}
	// Read sparse values.
	valBVIdx := sp.Values.BufferView
	if valBVIdx < 0 || int(valBVIdx) >= len(doc.glTF.BufferViews) {
		return nil, 0, 0, 0, fmt.Errorf("sparse values: invalid buffer view index %d", valBVIdx)
	}
	valBV := &doc.glTF.BufferViews[valBVIdx]
	if valBV.Buffer < 0 || int(valBV.Buffer) >= len(doc.bins) {
		return nil, 0, 0, 0, fmt.Errorf("sparse values: invalid buffer index %d", valBV.Buffer)
	}
	valBuf := doc.bins[valBV.Buffer]
	valStart := int(valBV.ByteOffset + sp.Values.ByteOffset)
	valEnd := valStart + int(sp.Count)*elemSize
	if valStart < 0 || valEnd > len(valBuf) {
		return nil, 0, 0, 0, errors.New("sparse values out of buffer bounds")
	}
	for j := 0; j < int(sp.Count); j++ {
		var elemIdx int
		switch sp.Indices.ComponentType {
		case gltf.UNSIGNED_BYTE:
			elemIdx = int(idxBuf[idxStart+j])
		case gltf.UNSIGNED_SHORT:
			elemIdx = int(binary.LittleEndian.Uint16(idxBuf[idxStart+j*2:]))
		case gltf.UNSIGNED_INT:
			elemIdx = int(binary.LittleEndian.Uint32(idxBuf[idxStart+j*4:]))
		default:
			return nil, 0, 0, 0, fmt.Errorf("sparse indices: unsupported component type %d", sp.Indices.ComponentType)
		}
		if elemIdx < 0 || elemIdx >= count {
			return nil, 0, 0, 0, fmt.Errorf("sparse index %d out of accessor range", elemIdx)
		}
		copy(base[elemIdx*elemSize:], valBuf[valStart+j*elemSize:valStart+j*elemSize+elemSize])
	}
	return base, elemSize, stride, count, nil
}

func gltfReadScalarFloat(data []byte, componentType gltf.ComponentType, normalized bool) (float32, error) {
	switch componentType {
	case gltf.BYTE:
		v := float32(int8(data[0]))
		if normalized {
			if v <= -128 {
				return -1, nil
			}
			return v / 127.0, nil
		}
		return v, nil
	case gltf.UNSIGNED_BYTE:
		v := float32(data[0])
		if normalized {
			return v / 255.0, nil
		}
		return v, nil
	case gltf.SHORT:
		v := float32(int16(binary.LittleEndian.Uint16(data[:2])))
		if normalized {
			if v <= -32768 {
				return -1, nil
			}
			return v / 32767.0, nil
		}
		return v, nil
	case gltf.UNSIGNED_SHORT:
		v := float32(binary.LittleEndian.Uint16(data[:2]))
		if normalized {
			return v / 65535.0, nil
		}
		return v, nil
	case gltf.UNSIGNED_INT:
		v := binary.LittleEndian.Uint32(data[:4])
		if normalized {
			return float32(float64(v) / float64(^uint32(0))), nil
		}
		return float32(v), nil
	case gltf.FLOAT:
		return math.Float32frombits(binary.LittleEndian.Uint32(data[:4])), nil
	default:
		return 0, fmt.Errorf("unsupported component type %d", componentType)
	}
}

func gltfReadScalarInt32(data []byte, componentType gltf.ComponentType) (int32, error) {
	switch componentType {
	case gltf.BYTE:
		return int32(int8(data[0])), nil
	case gltf.UNSIGNED_BYTE:
		return int32(data[0]), nil
	case gltf.SHORT:
		return int32(int16(binary.LittleEndian.Uint16(data[:2]))), nil
	case gltf.UNSIGNED_SHORT:
		return int32(binary.LittleEndian.Uint16(data[:2])), nil
	case gltf.UNSIGNED_INT:
		return int32(binary.LittleEndian.Uint32(data[:4])), nil
	default:
		return 0, fmt.Errorf("unsupported integer component type %d", componentType)
	}
}

func gltfReadAccessorFloats(doc *fullGLTF, acc *gltf.Accessor) ([]float32, error) {
	bytes, _, stride, count, err := gltfAccessorLayout(doc, acc)
	if err != nil {
		return nil, err
	}
	compCount, err := gltfAccessorComponentCount(acc)
	if err != nil {
		return nil, err
	}
	compSize, err := gltfComponentByteSize(acc.ComponentType)
	if err != nil {
		return nil, err
	}
	out := make([]float32, count*compCount)
	for i := 0; i < count; i++ {
		base := i * stride
		for c := 0; c < compCount; c++ {
			v, err := gltfReadScalarFloat(bytes[base+c*compSize:], acc.ComponentType, acc.Normalized)
			if err != nil {
				return nil, err
			}
			out[i*compCount+c] = v
		}
	}
	return out, nil
}

func gltfReadAccessorInts(doc *fullGLTF, acc *gltf.Accessor) ([]int32, error) {
	bytes, _, stride, count, err := gltfAccessorLayout(doc, acc)
	if err != nil {
		return nil, err
	}
	compCount, err := gltfAccessorComponentCount(acc)
	if err != nil {
		return nil, err
	}
	compSize, err := gltfComponentByteSize(acc.ComponentType)
	if err != nil {
		return nil, err
	}
	out := make([]int32, count*compCount)
	for i := 0; i < count; i++ {
		base := i * stride
		for c := 0; c < compCount; c++ {
			v, err := gltfReadScalarInt32(bytes[base+c*compSize:], acc.ComponentType)
			if err != nil {
				return nil, err
			}
			out[i*compCount+c] = v
		}
	}
	return out, nil
}

func gltfReadMeshMorphTargets(mesh *gltf.Mesh, doc *fullGLTF, primitive int, verts []rendering.Vertex) error {
	defer tracing.NewRegion("loaders.gltfReadMeshMorphTargets").End()
	if primitive < 0 || primitive >= len(mesh.Primitives) {
		return fmt.Errorf("invalid primitive index %d", primitive)
	}
	for _, target := range mesh.Primitives[primitive].Targets {
		if target.POSITION == nil {
			continue
		}
		acc, err := gltfAccessorByIntIndex(doc, *target.POSITION)
		if err != nil {
			return err
		}
		if acc.ComponentType != gltf.FLOAT || acc.Type != gltf.VEC3 {
			return errors.New("morph target position accessor must be float VEC3")
		}
		floats, err := gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return err
		}
		if int(acc.Count) != len(verts) || len(floats) != len(verts)*3 {
			return errors.New("morph targets do not match vert count")
		}
		for i := range verts {
			verts[i].MorphTarget = matrix.NewVec3(
				matrix.Float(floats[i*3+0]),
				matrix.Float(floats[i*3+1]),
				matrix.Float(floats[i*3+2]),
			)
		}
		return nil // Vertex currently only stores a single morph target.
	}
	return nil
}

func gltfReadMeshVerts(mesh *gltf.Mesh, doc *fullGLTF, primitive int) ([]rendering.Vertex, error) {
	defer tracing.NewRegion("loaders.gltfReadMeshVerts").End()
	if primitive < 0 || primitive >= len(mesh.Primitives) {
		return nil, fmt.Errorf("invalid primitive index %d", primitive)
	}
	prim := mesh.Primitives[primitive]
	posIdx, ok := gltfAttr(prim, gltf.POSITION)
	if !ok {
		return nil, errors.New("mesh primitive missing POSITION attribute")
	}
	posAcc, err := gltfAccessorByUintIndex(doc, posIdx)
	if err != nil {
		return nil, err
	}
	if posAcc.ComponentType != gltf.FLOAT || posAcc.Type != gltf.VEC3 {
		return nil, errors.New("POSITION accessor must be float VEC3")
	}
	positions, err := gltfReadAccessorFloats(doc, posAcc)
	if err != nil {
		return nil, err
	}
	vertCount := int(posAcc.Count)
	if vertCount <= 0 {
		return nil, errors.New("vertCount <= 0")
	}

	var normals []float32
	if idx, ok := gltfAttr(prim, gltf.NORMAL); ok {
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.ComponentType != gltf.FLOAT || acc.Type != gltf.VEC3 {
			return nil, errors.New("NORMAL accessor must be float VEC3")
		}
		normals, err = gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return nil, err
		}
	}

	var tangents []float32
	if idx, ok := gltfAttr(prim, gltf.TANGENT); ok {
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.ComponentType != gltf.FLOAT || acc.Type != gltf.VEC4 {
			return nil, errors.New("TANGENT accessor must be float VEC4")
		}
		tangents, err = gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return nil, err
		}
	}

	var texCoords0 []float32
	if idx, ok := gltfAttr(prim, gltf.TEXCOORD_0); ok {
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.Type != gltf.VEC2 {
			return nil, errors.New("TEXCOORD_0 accessor must be VEC2")
		}
		texCoords0, err = gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return nil, err
		}
	}

	var jointIDs []int32
	var weights []float32
	jointsPresent := false
	weightsPresent := false
	if idx, ok := gltfAttr(prim, gltf.JOINTS_0); ok {
		jointsPresent = true
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.Type != gltf.VEC4 {
			return nil, errors.New("JOINTS_0 accessor must be VEC4")
		}
		jointIDs, err = gltfReadAccessorInts(doc, acc)
		if err != nil {
			return nil, err
		}
	}
	if idx, ok := gltfAttr(prim, gltf.WEIGHTS_0); ok {
		weightsPresent = true
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.Type != gltf.VEC4 {
			return nil, errors.New("WEIGHTS_0 accessor must be VEC4")
		}
		weights, err = gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return nil, err
		}
	}
	if jointsPresent != weightsPresent {
		return nil, errors.New("JOINTS_0 and WEIGHTS_0 must both be present")
	}

	var colors0 []float32
	colors0IsVec4 := false
	if idx, ok := gltfAttr(prim, gltf.COLOR_0); ok {
		acc, err := gltfAccessorByUintIndex(doc, idx)
		if err != nil {
			return nil, err
		}
		if acc.Type != gltf.VEC3 && acc.Type != gltf.VEC4 {
			return nil, errors.New("COLOR_0 accessor must be VEC3 or VEC4")
		}
		colors0IsVec4 = acc.Type == gltf.VEC4
		colors0, err = gltfReadAccessorFloats(doc, acc)
		if err != nil {
			return nil, err
		}
	}

	vertData := make([]rendering.Vertex, vertCount)
	vertColor := matrix.ColorWhite()
	if prim.Material != nil && *prim.Material >= 0 && int(*prim.Material) < len(doc.glTF.Materials) {
		mat := doc.glTF.Materials[*prim.Material]
		if mat.PBRMetallicRoughness.BaseColorFactor != nil {
			vertColor = *mat.PBRMetallicRoughness.BaseColorFactor
		}
	}
	for i := 0; i < vertCount; i++ {
		vertData[i].Position = matrix.NewVec3(
			matrix.Float(positions[i*3+0]),
			matrix.Float(positions[i*3+1]),
			matrix.Float(positions[i*3+2]),
		)
		if colors0IsVec4 && len(colors0) >= (i+1)*4 {
			vertData[i].Color = matrix.Color{
				matrix.Float(colors0[i*4+0]) * vertColor[matrix.R],
				matrix.Float(colors0[i*4+1]) * vertColor[matrix.G],
				matrix.Float(colors0[i*4+2]) * vertColor[matrix.B],
				matrix.Float(colors0[i*4+3]) * vertColor[matrix.A],
			}
		} else if !colors0IsVec4 && len(colors0) >= (i+1)*3 {
			vertData[i].Color = matrix.Color{
				matrix.Float(colors0[i*3+0]) * vertColor[matrix.R],
				matrix.Float(colors0[i*3+1]) * vertColor[matrix.G],
				matrix.Float(colors0[i*3+2]) * vertColor[matrix.B],
				vertColor[matrix.A],
			}
		} else {
			vertData[i].Color = vertColor
		}
		vertData[i].MorphTarget = vertData[i].Position
		if len(jointIDs) >= (i+1)*4 {
			vertData[i].JointIds = matrix.Vec4i{jointIDs[i*4+0], jointIDs[i*4+1], jointIDs[i*4+2], jointIDs[i*4+3]}
		}
		if len(weights) >= (i+1)*4 {
			vertData[i].JointWeights = matrix.NewVec4(
				matrix.Float(weights[i*4+0]),
				matrix.Float(weights[i*4+1]),
				matrix.Float(weights[i*4+2]),
				matrix.Float(weights[i*4+3]),
			)
		} else {
			vertData[i].JointWeights = matrix.Vec4Zero()
		}
		if len(normals) >= (i+1)*3 {
			vertData[i].Normal = matrix.NewVec3(
				matrix.Float(normals[i*3+0]),
				matrix.Float(normals[i*3+1]),
				matrix.Float(normals[i*3+2]),
			)
		} else {
			vertData[i].Normal = matrix.Vec3Zero()
		}
		if len(tangents) >= (i+1)*4 {
			vertData[i].Tangent = matrix.NewVec4(
				matrix.Float(tangents[i*4+0]),
				matrix.Float(tangents[i*4+1]),
				matrix.Float(tangents[i*4+2]),
				matrix.Float(tangents[i*4+3]),
			)
		} else {
			vertData[i].Tangent = matrix.Vec4Zero()
		}
		if len(texCoords0) >= (i+1)*2 {
			vertData[i].UV0 = matrix.NewVec2(matrix.Float(texCoords0[i*2+0]), matrix.Float(texCoords0[i*2+1]))
		} else {
			vertData[i].UV0 = matrix.Vec2Zero()
		}
	}
	if err := gltfReadMeshMorphTargets(mesh, doc, primitive, vertData); err != nil {
		return nil, err
	}
	return vertData, nil
}

func gltfReadMeshIndices(mesh *gltf.Mesh, doc *fullGLTF, primitive int, vertexCount int) ([]uint32, error) {
	defer tracing.NewRegion("loaders.gltfReadMeshIndices").End()
	if primitive < 0 || primitive >= len(mesh.Primitives) {
		return nil, fmt.Errorf("invalid primitive index %d", primitive)
	}
	idxPtr := mesh.Primitives[primitive].Indices
	if idxPtr == nil {
		indices := make([]uint32, vertexCount)
		for i := range indices {
			indices[i] = uint32(i)
		}
		return indices, nil
	}
	acc, err := gltfAccessorByIntIndex(doc, *idxPtr)
	if err != nil {
		return nil, err
	}
	if acc.Type != gltf.SCALAR {
		return nil, errors.New("index accessor must be SCALAR")
	}
	switch acc.ComponentType {
	case gltf.UNSIGNED_BYTE, gltf.UNSIGNED_SHORT, gltf.UNSIGNED_INT:
		// valid
	default:
		return nil, errors.New("index accessor must use an unsigned integer component type")
	}
	values, err := gltfReadAccessorInts(doc, acc)
	if err != nil {
		return nil, err
	}
	out := make([]uint32, len(values))
	for i, v := range values {
		out[i] = uint32(v)
	}
	return out, nil
}

func gltfReadMeshTextures(mesh *gltf.Mesh, doc *gltf.GLTF, primitive int) map[string]string {
	defer tracing.NewRegion("loaders.gltfReadMeshTextures").End()
	textures := make(map[string]string)
	if primitive < 0 || primitive >= len(mesh.Primitives) || len(doc.Materials) == 0 || mesh.Primitives[primitive].Material == nil {
		return textures
	}
	uri := func(path string) string {
		if strings.HasPrefix(strings.ToLower(path), "data:") {
			return path
		}
		return filepath.ToSlash(filepath.Join(filepath.Dir(doc.Asset.FilePath), filepath.FromSlash(path)))
	}
	resolveTexture := func(texID *gltf.TextureId) string {
		if texID == nil || texID.Index < 0 || int(texID.Index) >= len(doc.Textures) {
			return ""
		}
		tex := doc.Textures[texID.Index]
		if tex.Source == nil || *tex.Source < 0 || int(*tex.Source) >= len(doc.Images) {
			return ""
		}
		img := doc.Images[*tex.Source]
		if img.URI != "" {
			return uri(img.URI)
		}
		// Embedded bufferView images require a byte-based texture API, not a path string.
		return ""
	}
	mat := doc.Materials[*mesh.Primitives[primitive].Material]
	if path := resolveTexture(mat.PBRMetallicRoughness.BaseColorTexture); path != "" {
		textures["baseColor"] = path
	}
	if path := resolveTexture(mat.PBRMetallicRoughness.MetallicRoughnessTexture); path != "" {
		textures["metallicRoughness"] = path
	}
	if path := resolveTexture(mat.NormalTexture); path != "" {
		textures["normal"] = path
	}
	if path := resolveTexture(mat.OcclusionTexture); path != "" {
		textures["occlusion"] = path
	}
	if path := resolveTexture(mat.EmissiveTexture); path != "" {
		textures["emissive"] = path
	}
	return textures
}

func gltfReadAnimations(doc *fullGLTF) []load_result.Animation {
	defer tracing.NewRegion("loaders.gltfReadAnimations").End()
	anims := make([]load_result.Animation, len(doc.glTF.Animations))
	for i := range doc.glTF.Animations {
		a := &doc.glTF.Animations[i]
		anims[i] = load_result.Animation{Name: a.Name, Frames: make([]load_result.AnimKeyFrame, 0)}
		for j := range a.Channels {
			c := a.Channels[j]
			if c.Sampler < 0 || int(c.Sampler) >= len(a.Samplers) {
				continue
			}
			sampler := &a.Samplers[c.Sampler]
			inAcc, err := gltfAccessorByIntIndex(doc, sampler.Input)
			if err != nil {
				continue
			}
			outAcc, err := gltfAccessorByIntIndex(doc, sampler.Output)
			if err != nil {
				continue
			}
			if inAcc.Type != gltf.SCALAR {
				continue
			}
			times, err := gltfReadAccessorFloats(doc, inAcc)
			if err != nil {
				continue
			}
			values, err := gltfReadAccessorFloats(doc, outAcc)
			if err != nil {
				continue
			}
			bone := load_result.AnimBone{
				PathType:      c.Target.Path(),
				Interpolation: sampler.Interpolation(),
				NodeIndex:     int(c.Target.Node),
			}
			if bone.Interpolation == load_result.AnimInterpolateInvalid {
				bone.Interpolation = load_result.AnimInterpolateLinear
			}
			components := 0
			switch bone.PathType {
			case load_result.AnimPathTranslation, load_result.AnimPathScale:
				components = 3
			case load_result.AnimPathRotation:
				components = 4
			case load_result.AnimPathWeights:
				// Current load_result.AnimBone only stores up to four components and has no morph target indexing.
				continue
			default:
				continue
			}
			valuesPerKey := components
			if bone.Interpolation == load_result.AnimInterpolateCubicSpline {
				valuesPerKey = components * 3
			}
			if len(values) < len(times)*valuesPerKey {
				continue
			}
			for k := 0; k < len(times); k++ {
				var key *load_result.AnimKeyFrame
				for l := range anims[i].Frames {
					if matrix.Approx(anims[i].Frames[l].Time, times[k]) {
						key = &anims[i].Frames[l]
						break
					}
				}
				if key == nil {
					anims[i].Frames = append(anims[i].Frames, load_result.AnimKeyFrame{Bones: make([]load_result.AnimBone, 0), Time: times[k]})
					key = &anims[i].Frames[len(anims[i].Frames)-1]
				}
				thisBone := bone
				valueOffset := k * valuesPerKey
				if bone.Interpolation == load_result.AnimInterpolateCubicSpline {
					valueOffset += components // skip in-tangent, keep the actual key value
				}
				switch bone.PathType {
				case load_result.AnimPathTranslation, load_result.AnimPathScale:
					vec := matrix.NewVec3(
						matrix.Float(values[valueOffset+0]),
						matrix.Float(values[valueOffset+1]),
						matrix.Float(values[valueOffset+2]),
					)
					thisBone.Data = vec.AsAligned16()
				case load_result.AnimPathRotation:
					q := matrix.QuaternionFromXYZW([4]matrix.Float{
						matrix.Float(values[valueOffset+0]),
						matrix.Float(values[valueOffset+1]),
						matrix.Float(values[valueOffset+2]),
						matrix.Float(values[valueOffset+3]),
					})
					thisBone.Data = [4]matrix.Float{q.W(), q.X(), q.Y(), q.Z()}
				}
				key.Bones = append(key.Bones, thisBone)
			}
		}
		slices.SortFunc(anims[i].Frames, func(a, b load_result.AnimKeyFrame) int {
			switch {
			case a.Time < b.Time:
				return -1
			case a.Time > b.Time:
				return 1
			default:
				return 0
			}
		})
		if len(anims[i].Frames) == 0 {
			continue
		}
		for j := 0; j < len(anims[i].Frames)-1; j++ {
			anims[i].Frames[j].Time = anims[i].Frames[j+1].Time - anims[i].Frames[j].Time
		}
		anims[i].Frames[len(anims[i].Frames)-1].Time = 0.0
	}
	return anims
}
