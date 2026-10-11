// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/draw"

// startNormalSprite resolves the command into the draw op and starts
// drawing its rows.
func (v *VDP1) startNormalSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpNormalSprite, cmd)
	characterOp(op, cmd)
	op.X[0] = int32(cmd.xa) + int32(v.localX)
	op.Y[0] = int32(cmd.ya) + int32(v.localY)

	if op.CharW == 0 || op.CharH == 0 {
		return 0, true
	}

	if cmd.pmod&0x0800 == 0 &&
		draw.PreClipReject(op.X[0], op.Y[0], op.X[0]+op.CharW-1, op.Y[0]+op.CharH-1, op.Pixel.ClipX, op.Pixel.ClipY) {
		return draw.PreClipLineCycles * op.CharH, true
	}

	return v.beginDraw(cmd, budget)
}

// startScaledSprite resolves the command into the draw op (zoom-point
// resolution, dest rectangle, effective flips, HSS state) and starts
// drawing its rows.
func (v *VDP1) startScaledSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpScaledSprite, cmd)
	characterOp(op, cmd)
	flipH := op.Flags&draw.OpFlagFlipH != 0
	flipV := op.Flags&draw.OpFlagFlipV != 0
	hss := cmd.pmod&0x1000 != 0

	if op.CharW == 0 || op.CharH == 0 {
		return 0, true
	}

	zp := (cmd.ctrl >> 8) & 0x0F
	lx := int(v.localX)
	ly := int(v.localY)

	var dstX1, dstY1, dstX2, dstY2 int
	var coordFlipH, coordFlipV bool

	if zp == 0 {
		// Two-coordinate mode: A=upper-left, C=lower-right
		ax := int(cmd.xa) + lx
		ay := int(cmd.ya) + ly
		cx := int(cmd.xc) + lx
		cy := int(cmd.yc) + ly

		coordFlipH = cx < ax
		coordFlipV = cy < ay

		if ax <= cx {
			dstX1, dstX2 = ax, cx
		} else {
			dstX1, dstX2 = cx, ax
		}
		if ay <= cy {
			dstY1, dstY2 = ay, cy
		} else {
			dstY1, dstY2 = cy, ay
		}
	} else {
		ax := int(cmd.xa) + lx
		ay := int(cmd.ya) + ly
		dispW := int(cmd.xb)
		dispH := int(cmd.yb)

		if dispW < 0 || dispH < 0 {
			return 0, true
		}

		zpH := zp & 0x3
		zpV := (zp >> 2) & 0x3
		if zpH == 0 || zpV == 0 {
			return 0, true
		}

		switch zpH {
		case 1:
			dstX1, dstX2 = ax, ax+dispW
		case 2:
			dstX1, dstX2 = ax-dispW/2, ax+(dispW+1)/2
		case 3:
			dstX1, dstX2 = ax-dispW, ax
		}

		switch zpV {
		case 1:
			dstY1, dstY2 = ay, ay+dispH
		case 2:
			dstY1, dstY2 = ay-dispH/2, ay+(dispH+1)/2
		case 3:
			dstY1, dstY2 = ay-dispH, ay
		}
	}

	if dstX2 < dstX1 || dstY2 < dstY1 {
		return 0, true
	}

	// The op carries the destination rectangle in A and C and the
	// flips combined with the coordinate order.
	op.X[0], op.Y[0] = int32(dstX1), int32(dstY1)
	op.X[2], op.Y[2] = int32(dstX2), int32(dstY2)
	op.Flags &^= draw.OpFlagFlipH | draw.OpFlagFlipV
	if flipH != coordFlipH {
		op.Flags |= draw.OpFlagFlipH
	}
	if flipV != coordFlipV {
		op.Flags |= draw.OpFlagFlipV
	}

	// HSS subsamples the source by parity (per FBCR.EOS) only along the
	// horizontal (X) read direction of a line being shrunk; vertical
	// sampling is unaffected (manual Fig 6.5 retains every source row,
	// odd rows included). At 1:1 or enlarged, sampling is unmodified.
	// The end-code-disable side-effect rides on the X-shrink case.
	if hss && draw.SpriteRowWidth(op) < op.CharW {
		op.Flags |= draw.OpFlagHSS
		if v.fbcr&0x10 != 0 {
			op.Flags |= draw.OpFlagHSSOdd
		}
	}

	if cmd.pmod&0x0800 == 0 && draw.PreClipReject(op.X[0], op.Y[0], op.X[2], op.Y[2], op.Pixel.ClipX, op.Pixel.ClipY) {
		return draw.PreClipLineCycles * draw.SpriteRows(op), true
	}

	return v.beginDraw(cmd, budget)
}
