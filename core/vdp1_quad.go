// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/draw"

// startDistortedSprite resolves the command into the draw op and
// starts drawing it: a quad of connecting lines between the A-D and
// B-C edges, sampling the character.
func (v *VDP1) startDistortedSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpDistortedSprite, cmd)
	characterOp(op, cmd)
	v.quadOp(op, cmd)
	if cmd.pmod&0x1000 != 0 {
		op.Flags |= draw.OpFlagHSS
		if v.fbcr&0x10 != 0 {
			op.Flags |= draw.OpFlagHSSOdd
		}
	}

	if op.CharW == 0 || op.CharH == 0 {
		return 0, true
	}

	minX, minY, maxX, maxY := draw.QuadBounds(op)
	if cmd.pmod&0x0800 == 0 && draw.PreClipReject(minX, minY, maxX, maxY, op.Pixel.ClipX, op.Pixel.ClipY) {
		return draw.PreClipLineCycles * (maxY - minY + 1), true
	}

	return v.beginDraw(cmd, budget)
}

// startPolygon resolves the command into the draw op and starts
// drawing it: a quad of connecting lines filled with a flat color,
// or the A-B segment when the side edges are points.
func (v *VDP1) startPolygon(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpPolygon, cmd)
	v.quadOp(op, cmd)

	minX, minY, maxX, maxY := draw.QuadBounds(op)
	if cmd.pmod&0x0800 == 0 && draw.PreClipReject(minX, minY, maxX, maxY, op.Pixel.ClipX, op.Pixel.ClipY) {
		return draw.PreClipLineCycles * (maxY - minY + 1), true
	}

	return v.beginDraw(cmd, budget)
}
