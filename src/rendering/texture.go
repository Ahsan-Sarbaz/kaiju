/******************************************************************************/
/* texture.go                                                                 */
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

package rendering

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"strings"

	"kaijuengine.com/engine/assets"
	"kaijuengine.com/matrix"
	"kaijuengine.com/platform/profiler/tracing"

	"github.com/KaijuEngine/uuid"
)

/*
	ASTC notes:
	The header size is found here:  https://github.com/ARM-software/astc-encoder/blob/437f2423fede947a09086f28f547d1897bfe4546/Source/astc_toplevel.cpp#L177

	The following struct denotes it:
	struct astc_header
	{
		uint8_t magic[4];
		uint8_t blockdim_x;
		uint8_t blockdim_y;
		uint8_t blockdim_z;
		uint8_t xsize[3];			// x-size = xsize[0] + xsize[1] + xsize[2]
		uint8_t ysize[3];			// x-size, y-size and z-size are given in texels;
		uint8_t zsize[3];			// block count is inferred
	};
*/

type TextureInputType int
type TextureColorFormat int
type TextureFilter = int
type TextureMemType = int
type TextureFileFormat = int
type TextureDimensions = int

const (
	TextureInputTypeCompressedRgbaAstc4x4 TextureInputType = iota
	TextureInputTypeCompressedRgbaAstc5x4
	TextureInputTypeCompressedRgbaAstc5x5
	TextureInputTypeCompressedRgbaAstc6x5
	TextureInputTypeCompressedRgbaAstc6x6
	TextureInputTypeCompressedRgbaAstc8x5
	TextureInputTypeCompressedRgbaAstc8x6
	TextureInputTypeCompressedRgbaAstc8x8
	TextureInputTypeCompressedRgbaAstc10x5
	TextureInputTypeCompressedRgbaAstc10x6
	TextureInputTypeCompressedRgbaAstc10x8
	TextureInputTypeCompressedRgbaAstc10x10
	TextureInputTypeCompressedRgbaAstc12x10
	TextureInputTypeCompressedRgbaAstc12x12
	TextureInputTypeRgba8
	TextureInputTypeRgb8
	TextureInputTypeLuminance
	TextureInputTypeCompressedBc1RgbUnorm
	TextureInputTypeCompressedBc1RgbSrgb
	TextureInputTypeCompressedBc1RgbaUnorm
	TextureInputTypeCompressedBc1RgbaSrgb
	TextureInputTypeCompressedBc2Unorm
	TextureInputTypeCompressedBc2Srgb
	TextureInputTypeCompressedBc3Unorm
	TextureInputTypeCompressedBc3Srgb
	TextureInputTypeCompressedBc4Unorm
	TextureInputTypeCompressedBc4Snorm
	TextureInputTypeCompressedBc5Unorm
	TextureInputTypeCompressedBc5Snorm
	TextureInputTypeCompressedBc6hUfloat
	TextureInputTypeCompressedBc6hSfloat
	TextureInputTypeCompressedBc7Unorm
	TextureInputTypeCompressedBc7Srgb
)

const (
	TextureColorFormatRgbaUnorm TextureColorFormat = iota
	TextureColorFormatRgbUnorm
	TextureColorFormatRgbaSrgb
	TextureColorFormatRgbSrgb
	TextureColorFormatLuminance
)

const (
	TextureFilterLinear TextureFilter = iota
	TextureFilterNearest
	TextureFilterMax
)

const (
	TextureMemTypeUnsignedByte TextureMemType = iota
)

const (
	TextureFileFormatAstc TextureFileFormat = iota
	TextureFileFormatPng
	TextureFileFormatRaw
	TextureFileFormatDds
)

const (
	bytesInPixel = 4
	CubeMapSides = 6
)

const (
	TextureDimensions2 TextureDimensions = iota
	TextureDimensions1
	TextureDimensions3
	TextureDimensionsCube
)

const (
	GenerateUniqueTextureKey = ""
)

type GPUImageWriteRequest struct {
	Region matrix.Vec4i
	Pixels []byte
}

type TextureMipLayout struct {
	Width  int
	Height int
	Offset int
	Size   int
}

type TextureData struct {
	Mem            []byte
	InternalFormat TextureInputType
	Format         TextureColorFormat
	Type           TextureMemType
	Width          int
	Height         int
	InputType      TextureFileFormat
	Dimensions     TextureDimensions
	Mips           []TextureMipLayout
}

type transparencyReadState int

const (
	transparencyReadStateNone transparencyReadState = iota
	transparencyReadStateRead
	transparencyReadStateFound
)

type Texture struct {
	Key               string
	TexturePixelCache []byte
	RenderId          TextureId
	Channels          int
	Filter            int
	MipLevels         int
	Width             int
	Height            int
	CacheInvalid      bool
	pendingData       *TextureData
	hasTransparency   transparencyReadState
}

func TextureKeys(textures []*Texture) []string {
	defer tracing.NewRegion("rendering.TextureKeys").End()
	keys := make([]string, len(textures))
	for i, t := range textures {
		keys[i] = t.Key
	}
	return keys
}

// ReadRawTextureData reads raw texture data from a byte slice based on the specified input type (ASTC, PNG, or RAW).
// It returns a TextureData struct containing the decoded pixel data, dimensions, and format information.
func ReadRawTextureData(mem []byte, inputType TextureFileFormat) TextureData {
	defer tracing.NewRegion("rendering.ReadRawTextureData").End()

	var res TextureData
	res.InputType = inputType

	astcFormatMap := map[[2]byte]TextureInputType{
		{4, 0}:   TextureInputTypeCompressedRgbaAstc4x4,
		{5, 4}:   TextureInputTypeCompressedRgbaAstc5x4,
		{5, 5}:   TextureInputTypeCompressedRgbaAstc5x5,
		{6, 5}:   TextureInputTypeCompressedRgbaAstc6x5,
		{6, 6}:   TextureInputTypeCompressedRgbaAstc6x6,
		{8, 5}:   TextureInputTypeCompressedRgbaAstc8x5,
		{8, 6}:   TextureInputTypeCompressedRgbaAstc8x6,
		{8, 8}:   TextureInputTypeCompressedRgbaAstc8x8,
		{10, 5}:  TextureInputTypeCompressedRgbaAstc10x5,
		{10, 6}:  TextureInputTypeCompressedRgbaAstc10x6,
		{10, 8}:  TextureInputTypeCompressedRgbaAstc10x8,
		{10, 10}: TextureInputTypeCompressedRgbaAstc10x10,
		{12, 10}: TextureInputTypeCompressedRgbaAstc12x10,
		{12, 12}: TextureInputTypeCompressedRgbaAstc12x12,
	}

	switch inputType {
	case TextureFileFormatAstc:
		key := [2]byte{mem[4], mem[5]}
		if format, ok := astcFormatMap[key]; ok {
			res.InternalFormat = format
		}

		res.Width = int(mem[9])<<16 | int(mem[8])<<8 | int(mem[7])
		res.Height = int(mem[12])<<16 | int(mem[11])<<8 | int(mem[10])

		res.Mem = mem[16:]
		res.Format = TextureColorFormatRgbaUnorm
		res.Type = TextureMemTypeUnsignedByte

	case TextureFileFormatPng:
		img, err := png.Decode(bytes.NewReader(mem))
		if err != nil {
			return res
		}

		b := img.Bounds()
		w, h := b.Dx(), b.Dy()

		if rgba, ok := img.(*image.RGBA); ok {
			res.Mem = rgba.Pix
		} else {
			dst := image.NewRGBA(image.Rect(0, 0, w, h))
			draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
			res.Mem = dst.Pix
		}

		res.Width = w
		res.Height = h
		res.InternalFormat = TextureInputTypeRgba8
		res.Format = TextureColorFormatRgbaUnorm
		res.Type = TextureMemTypeUnsignedByte

	case TextureFileFormatRaw:
		res.Mem = mem
		res.Width = 0
		res.Height = 0
		res.InternalFormat = TextureInputTypeRgba8
		res.Format = TextureColorFormatRgbaUnorm
		res.Type = TextureMemTypeUnsignedByte

	case TextureFileFormatDds:
		// DDS header layout (all fields little-endian):
		//   [0:4]     magic "DDS "
		//   [4:8]     dwSize (124)
		//   [8:12]    dwFlags
		//   [12:16]   dwHeight
		//   [16:20]   dwWidth
		//   [28:32]   dwMipMapCount
		//   [80:84]   ddspf.dwFlags  (DDPF_FOURCC=0x4, DDPF_RGB=0x40)
		//   [84:88]   ddspf.dwFourCC
		//   DX10 extended header starts at [128], data follows at [148]
		if len(mem) < 128 {
			return res
		}
		width := int(binary.LittleEndian.Uint32(mem[16:20]))
		height := int(binary.LittleEndian.Uint32(mem[12:16]))
		ddpfFlags := binary.LittleEndian.Uint32(mem[80:84])
		mipCount := int(binary.LittleEndian.Uint32(mem[28:32]))
		if mipCount == 0 {
			mipCount = 1
		}
		fourCC := mem[84:88]
		dataOffset := 128
		parsed := true
		swizzleMipData := false

		const ddpfFourCC = 0x4
		const ddpfRgb = 0x40
		const ddpfAlphaPixels = 0x1

		if bytes.Equal(fourCC, []byte("DX10")) {
			if len(mem) < 148 {
				return res
			}
			dxgiFormat := binary.LittleEndian.Uint32(mem[128:132])
			dataOffset = 148
			switch dxgiFormat {
			case 71: // DXGI_FORMAT_BC1_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc1RgbaUnorm
			case 72: // DXGI_FORMAT_BC1_UNORM_SRGB
				res.InternalFormat = TextureInputTypeCompressedBc1RgbaSrgb
			case 74: // DXGI_FORMAT_BC2_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc2Unorm
			case 75: // DXGI_FORMAT_BC2_UNORM_SRGB
				res.InternalFormat = TextureInputTypeCompressedBc2Srgb
			case 77: // DXGI_FORMAT_BC3_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc3Unorm
			case 78: // DXGI_FORMAT_BC3_UNORM_SRGB
				res.InternalFormat = TextureInputTypeCompressedBc3Srgb
			case 80: // DXGI_FORMAT_BC4_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc4Unorm
			case 81: // DXGI_FORMAT_BC4_SNORM
				res.InternalFormat = TextureInputTypeCompressedBc4Snorm
			case 83: // DXGI_FORMAT_BC5_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc5Unorm
			case 84: // DXGI_FORMAT_BC5_SNORM
				res.InternalFormat = TextureInputTypeCompressedBc5Snorm
			case 95: // DXGI_FORMAT_BC6H_UF16
				res.InternalFormat = TextureInputTypeCompressedBc6hUfloat
			case 96: // DXGI_FORMAT_BC6H_SF16
				res.InternalFormat = TextureInputTypeCompressedBc6hSfloat
			case 98: // DXGI_FORMAT_BC7_UNORM
				res.InternalFormat = TextureInputTypeCompressedBc7Unorm
			case 99: // DXGI_FORMAT_BC7_UNORM_SRGB
				res.InternalFormat = TextureInputTypeCompressedBc7Srgb
			case 28: // DXGI_FORMAT_R8G8B8A8_UNORM
				res.InternalFormat = TextureInputTypeRgba8
				res.Format = TextureColorFormatRgbaUnorm
			case 29: // DXGI_FORMAT_R8G8B8A8_UNORM_SRGB
				res.InternalFormat = TextureInputTypeRgba8
				res.Format = TextureColorFormatRgbaSrgb
			case 87: // DXGI_FORMAT_B8G8R8A8_UNORM
				res.InternalFormat = TextureInputTypeRgba8
				res.Format = TextureColorFormatRgbaUnorm
				swizzleMipData = true
			case 91: // DXGI_FORMAT_B8G8R8A8_UNORM_SRGB
				res.InternalFormat = TextureInputTypeRgba8
				res.Format = TextureColorFormatRgbaSrgb
				swizzleMipData = true
			default:
				parsed = false
			}
		} else if ddpfFlags&ddpfFourCC != 0 {
			// Legacy compressed FourCC
			switch string(fourCC) {
			case "DXT1":
				// DDPF_ALPHAPIXELS set -> 1-bit alpha (BC1 RGBA), absent -> opaque (BC1 RGB).
				// Using RGBA for an opaque DXT1 decodes color0<=color1 blocks as transparent
				// black instead of a lerped color, corrupting pixels across block rows.
				if ddpfFlags&ddpfAlphaPixels != 0 {
					res.InternalFormat = TextureInputTypeCompressedBc1RgbaUnorm
				} else {
					res.InternalFormat = TextureInputTypeCompressedBc1RgbUnorm
				}
			case "DXT3":
				res.InternalFormat = TextureInputTypeCompressedBc2Unorm
			case "DXT5":
				res.InternalFormat = TextureInputTypeCompressedBc3Unorm
			case "ATI1", "BC4U":
				res.InternalFormat = TextureInputTypeCompressedBc4Unorm
			case "ATI2", "BC5U":
				res.InternalFormat = TextureInputTypeCompressedBc5Unorm
			default:
				parsed = false
			}
		} else if ddpfFlags&ddpfRgb != 0 {
			// Legacy uncompressed RGB/RGBA: detect channel order via bit masks.
			//   dwRBitMask at [92:96], dwBBitMask at [100:104]
			rMask := binary.LittleEndian.Uint32(mem[92:96])
			res.InternalFormat = TextureInputTypeRgba8
			res.Format = TextureColorFormatRgbaUnorm
			if rMask == 0x00FF0000 {
				// BGRA ordering (R mask in bits 16-23) -> swizzle to RGBA
				swizzleMipData = true
			}
		} else {
			parsed = false
		}

		if !parsed {
			return res
		}

		res.Width = width
		res.Height = height
		if len(mem) < dataOffset {
			return TextureData{}
		}
		mips, pixels, ok := parseDDSMipLayouts(mem[dataOffset:], res.InternalFormat, width, height, mipCount)
		if !ok {
			return TextureData{}
		}
		if res.Mem == nil {
			res.Mem = pixels
		}
		res.Mips = mips
		res.Type = TextureMemTypeUnsignedByte
		if swizzleMipData && len(res.Mem) > 0 && len(res.Mips) > 0 {
			for _, mip := range res.Mips {
				swizzleBgraToRgba(res.Mem[mip.Offset : mip.Offset+mip.Size])
			}
		}
	}

	return res
}

func (t *Texture) createData(imgBuff []byte, overrideWidth, overrideHeight int, key string) TextureData {
	inputType := TextureFileFormatRaw
	// TODO:  Use the content system to pull the type from the key
	if strings.HasSuffix(key, ".astc") {
		inputType = TextureFileFormatAstc
	} else if strings.HasSuffix(key, ".png") {
		inputType = TextureFileFormatPng
	} else if len(imgBuff) > 4 && imgBuff[0] == '\x89' && imgBuff[1] == 'P' && imgBuff[2] == 'N' && imgBuff[3] == 'G' {
		inputType = TextureFileFormatPng
	} else if len(imgBuff) > 4 && imgBuff[0] == 'D' && imgBuff[1] == 'D' && imgBuff[2] == 'S' && imgBuff[3] == ' ' {
		inputType = TextureFileFormatDds
	}
	data := ReadRawTextureData(imgBuff, inputType)
	if data.Width == 0 {
		data.Width = overrideWidth
	}
	if data.Height == 0 {
		data.Height = overrideHeight
	}
	return data
}

func (t *Texture) create(imgBuff []byte) {
	data := t.createData(imgBuff, 0, 0, t.Key)
	t.pendingData = &data
	t.Width = data.Width
	t.Height = data.Height
}

func NewTexture(assetDb assets.Database, key string, filter TextureFilter) (*Texture, error) {
	defer tracing.NewRegion("rendering.NewTexture").End()
	key = selectKey(key)
	tex := &Texture{Key: key, Filter: filter}
	if assetDb.Exists(key) {
		if imgBuff, err := assetDb.Read(key); err != nil {
			return nil, err
		} else if len(imgBuff) == 0 {
			return nil, errors.New("no data in texture")
		} else {
			tex.create(imgBuff)
			return tex, nil
		}
	} else {
		return nil, errors.New("texture does not exist")
	}
}

func (t *Texture) Reload(assetDb assets.Database) error {
	t.RenderId = TextureId{}
	if assetDb.Exists(t.Key) {
		if imgBuff, err := assetDb.Read(t.Key); err != nil {
			return err
		} else if len(imgBuff) == 0 {
			return errors.New("no data in texture")
		} else {
			t.create(imgBuff)
			return nil
		}
	}
	return errors.New("texture does not exist")
}

func (t *Texture) triedToReadTransparency() bool {
	return t.hasTransparency != transparencyReadStateNone
}

func (t *Texture) ReadPendingDataForTransparency() bool {
	if t.hasTransparency == transparencyReadStateFound {
		return true
	}
	if t.triedToReadTransparency() || t.pendingData == nil {
		return false
	}
	t.hasTransparency = transparencyReadStateRead
	for i := 0; i < len(t.pendingData.Mem); i += 4 {
		if t.pendingData.Mem[i] != 255 {
			t.hasTransparency = transparencyReadStateFound
			break
		}
	}
	return t.hasTransparency == transparencyReadStateFound
}

func (t *Texture) DelayedCreate(device *GPUDevice) {
	defer tracing.NewRegion("Texture.DelayedCreate").End()
	if t.RenderId.IsValid() {
		return
	}
	device.SetupTexture(t, t.pendingData)
	t.pendingData = nil
}

func NewTextureFromImage(key string, data []byte, filter TextureFilter) (*Texture, error) {
	defer tracing.NewRegion("rendering.NewTextureFromImage").End()
	tex := &Texture{Key: key, Filter: filter}
	tex.create(data)
	return tex, nil
}

func NewTextureFromMemory(key string, data []byte, width, height int, filter TextureFilter) (*Texture, error) {
	defer tracing.NewRegion("rendering.NewTextureFromMemory").End()
	key = selectKey(key)
	tex := &Texture{Key: key, Filter: filter}
	tex.create(data)
	if tex.Width == 0 {
		tex.Width = width
	}
	if tex.Height == 0 {
		tex.Height = height
	}
	return tex, nil
}

func (t *Texture) ReadPixel(app *GPUApplication, x, y int) matrix.Color {
	defer tracing.NewRegion("Texture.ReadPixel").End()
	return app.FirstInstance().PrimaryDevice().TextureReadPixel(t, x, y)
}

func (t *Texture) ReadAllPixels(app *GPUApplication) ([]byte, error) {
	defer tracing.NewRegion("Texture.ReadPixel").End()
	return app.FirstInstance().PrimaryDevice().TextureRead(t)
}

func (t *Texture) WritePixels(device *GPUDevice, requests []GPUImageWriteRequest) {
	defer tracing.NewRegion("Texture.WritePixels").End()
	device.TextureWritePixels(t, requests)
}

func (t Texture) Size() matrix.Vec2 {
	return matrix.Vec2{float32(t.Width), float32(t.Height)}
}

func (t *Texture) SetPendingDataDimensions(dim TextureDimensions) {
	if t.pendingData != nil {
		t.pendingData.Dimensions = dim
	}
}

func TexturePixelsFromAsset(assetDb assets.Database, key string) (TextureData, error) {
	defer tracing.NewRegion("rendering.TexturePixelsFromAsset").End()
	key = selectKey(key)
	if assetDb.Exists(key) {
		if imgBuff, err := assetDb.Read(key); err != nil {
			return TextureData{}, err
		} else if len(imgBuff) == 0 {
			return TextureData{}, errors.New("no data in texture")
		} else {
			return ReadRawTextureData(imgBuff, TextureFileFormatPng), nil
		}
	} else {
		return TextureData{}, errors.New("texture does not exist")
	}
}

func selectKey(req string) string {
	if req == GenerateUniqueTextureKey {
		return uuid.NewString()
	}
	return req
}

// swizzleBgraToRgba swaps the R and B channels in-place for a packed BGRA byte slice.
func swizzleBgraToRgba(p []byte) {
	for i := 0; i+3 < len(p); i += 4 {
		p[i], p[i+2] = p[i+2], p[i]
	}
}

func parseDDSMipLayouts(src []byte, inputType TextureInputType, width, height, mipCount int) ([]TextureMipLayout, []byte, bool) {
	mips := make([]TextureMipLayout, 0, mipCount)
	totalSize := 0
	offset := 0
	for level := range mipCount {
		mipWidth := max(width>>level, 1)
		mipHeight := max(height>>level, 1)
		size := textureMipByteSize(inputType, mipWidth, mipHeight)
		if size <= 0 || offset+size > len(src) {
			return nil, nil, false
		}
		mips = append(mips, TextureMipLayout{
			Width:  mipWidth,
			Height: mipHeight,
			Offset: totalSize,
			Size:   size,
		})
		totalSize += size
		offset += size
	}
	data := make([]byte, totalSize)
	offset = 0
	for _, mip := range mips {
		copy(data[mip.Offset:mip.Offset+mip.Size], src[offset:offset+mip.Size])
		offset += mip.Size
	}
	return mips, data, true
}

func textureMipByteSize(inputType TextureInputType, width, height int) int {
	blockWidth, blockHeight, blockBytes, compressed := textureBlockInfo(inputType)
	if compressed {
		blocksWide := (width + blockWidth - 1) / blockWidth
		blocksHigh := (height + blockHeight - 1) / blockHeight
		return blocksWide * blocksHigh * blockBytes
	}
	switch inputType {
	case TextureInputTypeRgba8:
		return width * height * 4
	case TextureInputTypeRgb8:
		return width * height * 3
	case TextureInputTypeLuminance:
		return width * height
	default:
		return 0
	}
}

func textureBlockInfo(inputType TextureInputType) (blockWidth, blockHeight, blockBytes int, compressed bool) {
	switch inputType {
	case TextureInputTypeCompressedBc1RgbUnorm,
		TextureInputTypeCompressedBc1RgbSrgb,
		TextureInputTypeCompressedBc1RgbaUnorm,
		TextureInputTypeCompressedBc1RgbaSrgb,
		TextureInputTypeCompressedBc4Unorm,
		TextureInputTypeCompressedBc4Snorm:
		return 4, 4, 8, true
	case TextureInputTypeCompressedBc2Unorm,
		TextureInputTypeCompressedBc2Srgb,
		TextureInputTypeCompressedBc3Unorm,
		TextureInputTypeCompressedBc3Srgb,
		TextureInputTypeCompressedBc5Unorm,
		TextureInputTypeCompressedBc5Snorm,
		TextureInputTypeCompressedBc6hUfloat,
		TextureInputTypeCompressedBc6hSfloat,
		TextureInputTypeCompressedBc7Unorm,
		TextureInputTypeCompressedBc7Srgb:
		return 4, 4, 16, true
	case TextureInputTypeCompressedRgbaAstc4x4:
		return 4, 4, 16, true
	case TextureInputTypeCompressedRgbaAstc5x4:
		return 5, 4, 16, true
	case TextureInputTypeCompressedRgbaAstc5x5:
		return 5, 5, 16, true
	case TextureInputTypeCompressedRgbaAstc6x5:
		return 6, 5, 16, true
	case TextureInputTypeCompressedRgbaAstc6x6:
		return 6, 6, 16, true
	case TextureInputTypeCompressedRgbaAstc8x5:
		return 8, 5, 16, true
	case TextureInputTypeCompressedRgbaAstc8x6:
		return 8, 6, 16, true
	case TextureInputTypeCompressedRgbaAstc8x8:
		return 8, 8, 16, true
	case TextureInputTypeCompressedRgbaAstc10x5:
		return 10, 5, 16, true
	case TextureInputTypeCompressedRgbaAstc10x6:
		return 10, 6, 16, true
	case TextureInputTypeCompressedRgbaAstc10x8:
		return 10, 8, 16, true
	case TextureInputTypeCompressedRgbaAstc10x10:
		return 10, 10, 16, true
	case TextureInputTypeCompressedRgbaAstc12x10:
		return 12, 10, 16, true
	case TextureInputTypeCompressedRgbaAstc12x12:
		return 12, 12, 16, true
	default:
		return 0, 0, 0, false
	}
}
