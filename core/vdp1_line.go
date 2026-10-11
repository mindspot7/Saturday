// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/draw"

// startLine resolves the command into the draw op (endpoints in A
// and B) and starts drawing it.
func (v *VDP1) startLine(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpLine, cmd)
	lx, ly := int32(v.localX), int32(v.localY)
	op.X[0], op.Y[0] = int32(cmd.xa)+lx, int32(cmd.ya)+ly
	op.X[1], op.Y[1] = int32(cmd.xb)+lx, int32(cmd.yb)+ly

	if cmd.pmod&0x0800 == 0 {
		minX, maxX := op.X[0], op.X[1]
		if maxX < minX {
			minX, maxX = maxX, minX
		}
		minY, maxY := op.Y[0], op.Y[1]
		if maxY < minY {
			minY, maxY = maxY, minY
		}
		if draw.PreClipReject(minX, minY, maxX, maxY, op.Pixel.ClipX, op.Pixel.ClipY) {
			return draw.PreClipLineCycles, true
		}
	}

	// The table is zero when the color calculation mode has no
	// Gouraud shading, so the endpoints are zero then.
	return v.beginDraw(cmd, budget)
}

// startPolyline resolves the command into the draw op (the four
// vertices) and starts drawing its legs, A-B, B-C, C-D, D-A.
func (v *VDP1) startPolyline(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	op := &v.cmd.op
	v.beginOp(op, draw.OpPolyline, cmd)
	v.quadOp(op, cmd)

	minX, minY, maxX, maxY := draw.QuadBounds(op)
	if cmd.pmod&0x0800 == 0 && draw.PreClipReject(minX, minY, maxX, maxY, op.Pixel.ClipX, op.Pixel.ClipY) {
		return draw.PreClipLineCycles * (maxY - minY + 1), true
	}

	return v.beginDraw(cmd, budget)
}
