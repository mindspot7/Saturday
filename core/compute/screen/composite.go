// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// Layer buffer indices for CompositeParams.LayerBuf.
const (
	LayerNBG0 = 0
	LayerNBG1 = 1
	LayerNBG2 = 2
	LayerNBG3 = 3
	LayerRBG0 = 4
	LayerRBG1 = 5
)

// CompositeInputs are the buffers the composite reads: the scroll
// layer buffers indexed by the Layer constants, the rotation screens'
// line color buffers (RBG0 then RBG1), the VDP1 frame buffer the
// sprite layer reads, the palette, and the VDP2 VRAM holding the
// line color table.
type CompositeInputs struct {
	Layers     [6]compute.Layer
	LineColors [2]compute.LineColor
	Sprite     compute.VDP1FB
	Palette    *compute.Palette
	VRAM       *compute.VDP2VRAM
}

// CompositeParams is the composite's configuration for one row.
type CompositeParams struct {
	Width          int32
	Y              int32  // the layer and line color buffer row read
	FBRow          int32  // output frame row
	FieldBootstrap uint32 // row xor mask of the other field's row, also written (first LSMD3 frame): 0 off, 1 native, N at scale N
	BackRGB        uint32 // back screen decoded color

	// Enabled scroll layers in candidate order: the buffer index, the
	// layer ID for windows and registers (0-3 NBG, 4 RBG0; RBG1 takes
	// 0), and the tie rank. Static: no layer has per-dot priority, so
	// the list is in display order.
	NLayers  int32
	LayerBuf [5]uint32
	LayerID  [5]uint32
	LayerTie [5]int32
	Static   bool

	// Windows: the row's runs and, per run, each window's state (0-3
	// NBG, 4 RBG0, 5 sprite, 6 color calculation).
	Runs     WindowRuns
	RunState [5][7]WindowState

	// Sprite layer.
	Sprite      SpriteDecode
	SpriteRow   SpriteRow
	SWBit       uint32 // frame buffer bit that is the sprite window bit
	SpriteCCOff bool   // hi-res palette restriction applies to sprites

	// Color calculation.
	CCMD       bool // add as is
	CCRTMD     bool // ratio from the second image
	EXCCEN     bool // extended color calculation
	LCCCEN     bool // line color screen's color calculation enable
	LayerIsRGB [6]bool
	LayerRatio [5]uint32
	CCRLB      uint32
	LNCLEN     uint32
	LCAddr     uint32 // the row's line color table entry address
	R1On       bool   // RBG1 occupies the NBG0 slot
	HiRes      bool
	CRAMMode   uint32

	// Color offset: enables and A/B selects per layer (bit 5 the back
	// screen, bit 6 the sprite layer), and the two offsets.
	CLOFEN uint32
	CLOFSL uint32
	COA    [3]int32
	COB    [3]int32

	SDCTL uint32
}

// candidate is one non-transparent layer pixel competing for the dot.
type candidate struct {
	pri     uint32
	tie     int32 // higher wins on equal priority
	layerID uint32
	rgb     uint32
	ccEn    bool
	ccBits  uint32 // sprite color calculation register index
}

func clampU8(v int32) uint32 {
	if v > 255 {
		return 255
	}
	if v < 0 {
		return 0
	}
	return uint32(v)
}

// ranksAbove reports whether candidate a displays above b: higher
// priority, then higher tie rank.
func ranksAbove(a, b *candidate) bool {
	return a.pri > b.pri || (a.pri == b.pri && a.tie > b.tie)
}

// putDot writes an opaque output pixel, the decoded color with bits
// 31:24 set, at off and, under the field bootstrap, at other.
func putDot(out compute.Framebuffer, off, other uint32, boot bool, r, g, b uint32) {
	v := 0xFF000000 | PackColor(r, g, b)
	out[off] = v
	if boot {
		out[other] = v
	}
}

// CompositeSpan composes dots [x0, x1) of the row into the output
// frame: per dot the candidate gathering across the enabled layers
// and the sprite, the top three images by priority (Table 11.1),
// color calculation with the second image (Sec 12), color offset, and
// shadow (Sec 14), and one store of the result. The back screen is
// the bottom of the stack and is held as a value, so the frame is
// never read. Under LSMD=3 the first frame in a mode also writes the
// other field's row, so the display never shows a half-bright frame.
// Entry point.
func CompositeSpan(in *CompositeInputs, out compute.Framebuffer, p *CompositeParams, x0, x1 int32) {
	width := p.Width
	rowBase := uint32(p.FBRow * width)
	boot := p.FieldBootstrap != 0
	otherBase := uint32((p.FBRow ^ int32(p.FieldBootstrap)) * width)
	br, bg, bb := ColorR(p.BackRGB), ColorG(p.BackRGB), ColorB(p.BackRGB)

	static := p.Static
	ccmd := p.CCMD
	ccrtmd := p.CCRTMD
	exccen := p.EXCCEN
	r1on := p.R1On
	hiResPalette := p.HiRes && p.CRAMMode >= 1

	for run := range p.Runs.N {
		rr := p.Runs.Runs[run]
		rx0, rx1 := rr.X0, rr.X1
		if rx0 < x0 {
			rx0 = x0
		}
		if rx1 > x1 {
			rx1 = x1
		}
		if rx0 >= rx1 {
			continue
		}
		wm := p.RunState[run&7]

		for x := rx0; x < rx1; x++ {
			pi := uint32(p.Y*width + x)
			off := rowBase + uint32(x)
			other := otherBase + uint32(x)

			var cands [6]candidate
			ncand := int32(0)

			// Sprite layer (layerID 5). Its frame buffer MSB is also the
			// sprite window bit (Sec 8.1); a transparent pixel reads as
			// zero and is never inside.
			shadowType := uint32(ShadowNone)
			var spCand candidate
			haveSp := false
			var spPixel uint32
			switch p.SpriteRow.Mode {
			case SpriteRead16:
				spPixel = SpritePixel16(in.Sprite, &p.SpriteRow, x)
			case SpriteRead8:
				spPixel = SpritePixel8(in.Sprite, &p.SpriteRow, x)
			case SpriteReadRotated:
				spPixel = SpritePixelRotated(in.Sprite, &p.SpriteRow, x)
			}
			swInside := spPixel&p.SWBit != 0
			if spPixel != 0 {
				stype := p.Sprite.Shadow(spPixel)
				if stype != ShadowNone {
					shadowType = stype
				}
				// A sprite shadow (MSB shadow with non-zero dot color,
				// Sec 14.1) is an already-written sprite with a shadow
				// added: it competes as a normal sprite pixel and is
				// halved when it is the top image. The other shadow
				// kinds have no pixel of their own.
				if stype == ShadowNone || stype == ShadowMSBSprite {
					pri, ccBits, colorMSB, rgb := p.Sprite.Decode(in.Palette, p.CRAMMode, spPixel)
					// Priority 0 is transparent (Sec 11.1); games disable
					// the sprite layer by writing PRISA-D = 0.
					if pri != 0 {
						spCand = candidate{pri, 5, 5, rgb, p.Sprite.CCEnabled(pri, colorMSB), ccBits}
						haveSp = true
					}
				}
			}

			// Scroll layers in row order.
			for i := range p.NLayers {
				px := in.Layers[p.LayerBuf[i]][pi]
				if px == 0 {
					continue
				}
				id := p.LayerID[i]
				if wm[id&7].Active(swInside) {
					continue
				}
				cands[ncand] = candidate{(px >> 24) & 0x07, p.LayerTie[i], id, px & ColorMask, px&LayerCCBit != 0, 0}
				ncand++
			}
			if haveSp {
				if static {
					// The scroll candidates are in display order; the
					// sprite ranks above every candidate at or below its
					// priority (sprite tie rank 5 wins equal priority).
					j := int32(0)
					for j < ncand && cands[j].pri > spCand.pri {
						j++
					}
					for k := ncand; k > j; k-- {
						cands[k] = cands[k-1]
					}
					cands[j] = spCand
				} else {
					cands[ncand] = spCand
				}
				ncand++
			}

			if ncand == 0 {
				// Back screen: color offset (CLOFEN bit 5) and shadow
				// (SDCTL bit 5, BKSDEN).
				cr, cg, cb := br, bg, bb
				if p.CLOFEN&(1<<5) != 0 {
					o := p.COA
					if p.CLOFSL&(1<<5) != 0 {
						o = p.COB
					}
					cr, cg, cb = clampU8(int32(br)+o[0]), clampU8(int32(bg)+o[1]), clampU8(int32(bb)+o[2])
				}
				if (shadowType == ShadowNormal || shadowType == ShadowMSBTransp) && p.SDCTL&(1<<5) != 0 {
					cr, cg, cb = cr/2, cg/2, cb/2
				}
				putDot(out, off, other, boot, cr, cg, cb)
				continue
			}

			// Top three images by priority then tie rank. A static row's
			// candidates are already in that order.
			topIdx, secIdx, thirdIdx := int32(0), int32(-1), int32(-1)
			if static {
				if ncand > 1 {
					secIdx = 1
				}
				if ncand > 2 {
					thirdIdx = 2
				}
			} else {
				for i := int32(1); i < ncand; i++ {
					if ranksAbove(&cands[i], &cands[topIdx]) {
						topIdx = i
					}
				}
				for i := range ncand {
					if i == topIdx {
						continue
					}
					if secIdx < 0 || ranksAbove(&cands[i], &cands[secIdx]) {
						secIdx = i
					}
				}
				for i := range ncand {
					if i == topIdx || i == secIdx {
						continue
					}
					if thirdIdx < 0 || ranksAbove(&cands[i], &cands[thirdIdx]) {
						thirdIdx = i
					}
				}
			}
			top := cands[topIdx]
			r := int32(ColorR(top.rgb))
			g := int32(ColorG(top.rgb))
			b := int32(ColorB(top.rgb))

			// Hi-res palette restriction: CRAM mode 1 or 2 disables
			// palette color calculation. Scroll layers always use
			// palette format; sprites do in all-palette mode.
			topCC := top.ccEn
			if topCC && hiResPalette && (top.layerID != 5 || p.SpriteCCOff) {
				topCC = false
			}

			if topCC && !wm[6].Active(swInside) {
				// Second image: the line color screen, the next candidate,
				// or the back screen, which is the bottom of the stack
				// (Sec 12.1, Figure 12.2).
				var secR, secG, secB int32
				lncInserted := false
				secIsBack := false
				if p.LNCLEN&(1<<top.layerID) != 0 {
					var lcData uint32
					if top.layerID == 4 {
						lcData = uint32(in.LineColors[0].U8(pi))
					} else if top.layerID == 0 && r1on {
						lcData = uint32(in.LineColors[1].U8(pi))
					}
					table := uint32(in.VRAM.U16BE(p.LCAddr))
					var addr uint32
					if lcData&0x80 != 0 {
						// Coefficient line color: the table's upper 4 bits
						// over the coefficient's 7 bits.
						addr = (table>>7&0x0F)<<7 | lcData&0x7F
					} else {
						// The table holds an 11-bit CRAM address (Figure 7.3).
						addr = table & 0x07FF
					}
					rgb, _ := PaletteColor(in.Palette, p.CRAMMode, addr)
					secR, secG, secB = int32(ColorR(rgb)), int32(ColorG(rgb)), int32(ColorB(rgb))
					lncInserted = true
				} else if secIdx >= 0 {
					sec := cands[secIdx]
					secR, secG, secB = int32(ColorR(sec.rgb)), int32(ColorG(sec.rgb)), int32(ColorB(sec.rgb))
				} else {
					secR, secG, secB = int32(br), int32(bg), int32(bb)
					secIsBack = true
				}

				// Extended color calculation: blend the second image
				// with the third (and fourth). In CRAM mode 1 or 2 a
				// palette-format second image does not blend (Table
				// 12.2).
				if exccen {
					secCC := false
					if secIsBack {
						secCC = false
					} else if lncInserted {
						secCC = p.LCCCEN
					} else if secIdx >= 0 {
						secCC = cands[secIdx].ccEn
					}
					if p.CRAMMode >= 1 && secIdx >= 0 && !p.LayerIsRGB[cands[secIdx].layerID&7] {
						secCC = false
					}
					if secCC {
						if !lncInserted {
							if thirdIdx >= 0 {
								t := cands[thirdIdx]
								secR = (secR + int32(ColorR(t.rgb))) / 2
								secG = (secG + int32(ColorG(t.rgb))) / 2
								secB = (secB + int32(ColorB(t.rgb))) / 2
							}
						} else {
							// The line color is the second image; the
							// original second is the third and its enable
							// gates the fourth. A palette-format fourth
							// image in CRAM mode 1 or 2 does not add.
							thirdCC := false
							if secIdx >= 0 {
								thirdCC = cands[secIdx].ccEn
							}
							if p.CRAMMode >= 1 && thirdIdx >= 0 && !p.LayerIsRGB[cands[thirdIdx].layerID&7] {
								thirdCC = false
							}
							if !thirdCC {
								if secIdx >= 0 {
									o := cands[secIdx]
									secR = (secR + int32(ColorR(o.rgb))) / 2
									secG = (secG + int32(ColorG(o.rgb))) / 2
									secB = (secB + int32(ColorB(o.rgb))) / 2
								}
							} else {
								// Ratio 2:1:1 in CRAM mode 1 or 2; mode 0 caps
								// at 2:1:0, dropping the fourth term.
								var o2r, o2g, o2b, o3r, o3g, o3b int32
								if secIdx >= 0 {
									o := cands[secIdx]
									o2r, o2g, o2b = int32(ColorR(o.rgb)), int32(ColorG(o.rgb)), int32(ColorB(o.rgb))
								}
								if p.CRAMMode >= 1 && thirdIdx >= 0 {
									o := cands[thirdIdx]
									o3r, o3g, o3b = int32(ColorR(o.rgb)), int32(ColorG(o.rgb)), int32(ColorB(o.rgb))
								}
								secR = secR/2 + o2r/4 + o3r/4
								secG = secG/2 + o2g/4 + o3g/4
								secB = secB/2 + o2b/4 + o3b/4
							}
						}
					}
				}

				if ccmd {
					r = int32(clampU8(r + secR))
					g = int32(clampU8(g + secG))
					b = int32(clampU8(b + secB))
				} else {
					var ratio int32
					if !ccrtmd {
						if top.layerID == 5 {
							ratio = int32(p.Sprite.Ratio[top.ccBits&7])
						} else {
							ratio = int32(p.LayerRatio[top.layerID&7])
						}
					} else if lncInserted {
						ratio = int32(p.CCRLB & 0x1F)
					} else if secIsBack {
						ratio = int32(p.CCRLB >> 8 & 0x1F)
					} else {
						sec := cands[secIdx]
						if sec.layerID == 5 {
							ratio = int32(p.Sprite.Ratio[sec.ccBits&7])
						} else {
							ratio = int32(p.LayerRatio[sec.layerID&7])
						}
					}
					r = (r*(31-ratio) + secR*(ratio+1)) / 32
					g = (g*(31-ratio) + secG*(ratio+1)) / 32
					b = (b*(31-ratio) + secB*(ratio+1)) / 32
				}
			}

			// Color offset per layer (bit 6 for the sprite layer).
			layerBit := top.layerID
			if layerBit == 5 {
				layerBit = 6
			}
			if p.CLOFEN&(1<<layerBit) != 0 {
				o := p.COA
				if p.CLOFSL&(1<<layerBit) != 0 {
					o = p.COB
				}
				r = int32(clampU8(r + o[0]))
				g = int32(clampU8(g + o[1]))
				b = int32(clampU8(b + o[2]))
			}

			// Shadow: normal and MSB transparent shadows darken the
			// scroll screen below per SDCTL; a sprite shadow displays
			// the sprite itself at half brightness (Sec 14.1).
			switch shadowType {
			case ShadowNormal, ShadowMSBTransp:
				if top.layerID != 5 && top.layerID <= 4 && p.SDCTL&(1<<top.layerID) != 0 {
					r, g, b = r/2, g/2, b/2
				}
			case ShadowMSBSprite:
				if top.layerID == 5 {
					r, g, b = r/2, g/2, b/2
				}
			}
			putDot(out, off, other, boot, uint32(r), uint32(g), uint32(b))
		}
	}
}
