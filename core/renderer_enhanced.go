// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
)

// enhancedRenderer renders on the walker as the software renderer
// does, at N times the native resolution on both axes (N = 1 <<
// shift): at each row's setup point it derives the row's N sub-row
// records from the VDP2's record (scaleRow), and at the row's render
// point each sub-row renders through the accurate compute functions
// into the renderer's own layer buffers and output frame buffer.
//
// It draws the VDP1 frame buffer into a pair of its own at N times
// each axis while the walk keeps the CPU pair: after the VDP1's tick
// of every line it takes the CPU's frame buffer writes and the list's
// entries appended since its last take, as the GPU renderer does, and
// applies them in order, the writes first, since the tick drew the
// line's entries after the writes landed. A write expands each byte
// to its block (CopyFBPages), and each entry is scaled (scaleOp) and
// drawn in the store pass one at a time in the list's order, the
// walk's own order.
type enhancedRenderer struct {
	vdp2              *VDP2
	vdp1              *VDP1
	shift             uint32
	in                screen.CompositeInputs // its layer and line color buffers, the VDP2's palette and VRAM
	outputFramebuffer compute.Framebuffer
	rows              []lineRecord      // the current row's sub-row records
	vdp1FB            [2]compute.VDP1FB // the pair, by physical index
	fbWrites          draw.FBWrites     // a take's CPU frame buffer write pages
	pages             []uint32          // a take's pages, reused
}

// newEnhancedRenderer opens the enhanced renderer at the scale shift:
// the layer and line color buffers hold the N sub-rows of one row, the
// output frame buffer the frame the VDP2 has latched at N times each
// axis, starting black, and the pair the frame buffers at N times each
// axis. From here the VDP1 keeps the CPU's frame buffer writes for the
// renderer's take, every byte of both buffers marked written, so the
// first take expands the whole CPU pair into the pair.
func newEnhancedRenderer(v *VDP2, v1 *VDP1, shift uint32) *enhancedRenderer {
	e := &enhancedRenderer{
		vdp2:  v,
		vdp1:  v1,
		shift: shift,
		rows:  make([]lineRecord, 1<<shift),
		fbWrites: draw.FBWrites{
			Store: new(compute.VDP1PageStore),
			Masks: new(compute.MaskStore),
			Table: new(compute.PageTable),
		},
		pages: make([]uint32, 0, vdp1FBPages),
	}
	s := e.grow()
	for i := range e.vdp1FB {
		e.vdp1FB[i] = make(compute.VDP1FB, s.pairBytes)
	}
	e.in.Sprite = noSpriteFB
	e.in.Palette = &v.palette
	e.in.VRAM = (*compute.VDP2VRAM)(v.vram)
	v1.fbWritesTaken = true
	v1.resyncFB()
	return e
}

// grow gives the layer, line color, and output frame buffers the
// lengths the latched frame needs, one row slot per sub-row, and
// returns the sizes.
func (e *enhancedRenderer) grow() renderSizes {
	var s renderSizes
	e.outputFramebuffer, s = growRenderBuffers(e.vdp2, e.shift, len(e.rows), &e.in, e.outputFramebuffer)
	return s
}

// beginFrame grows the buffers when BeginFrame latched a new geometry,
// then blanks the scaled displayed rows of the output frame buffer
// when the VDP2 blanked the frame (a geometry change or the display
// off).
func (e *enhancedRenderer) beginFrame() {
	v := e.vdp2
	if v.frame.resized {
		e.grow()
	}
	if v.frame.blanked {
		blankFramebuffer(e.outputFramebuffer, (v.DisplayHeight()<<e.shift)*(v.frame.width<<e.shift))
	}
}

// beginLine derives row y's sub-row records, each placed at its own
// row slot (sub-row k at slot k), and writes each sub-row's inputs the
// chain names that the row setup does not into its slot (rowInputs).
func (e *enhancedRenderer) beginLine(y int) {
	v := e.vdp2
	if !v.lineActive {
		return
	}
	native := v.rowRecord(y)
	for k := range e.rows {
		v.scaleRow(&e.rows[k], native, int32(k), e.shift)
		placeRow(&e.rows[k], int32(k))
		v.rowInputs(native, &e.in.Layers, &e.in.LineColors, k, y, k, e.shift)
	}
}

// renderLine renders the row's sub-rows, the sprite layer read from
// the half of the pair the row's VDP1 frame buffer view names by
// physical index, or noSpriteFB when the row has none.
func (e *enhancedRenderer) renderLine(int) {
	v := e.vdp2
	if !v.lineActive {
		return
	}
	e.in.Sprite = noSpriteFB
	if v.lineFB.data != nil {
		e.in.Sprite = e.vdp1FB[e.vdp1.physicalBuffer(v.lineFB.data)]
	}
	width := int32(v.frame.width) << e.shift
	for k := range e.rows {
		v.renderRecord(&e.rows[k], &e.in, e.outputFramebuffer, width)
	}
}

// vdp1Line runs after the VDP1's tick of a line: it takes the CPU's
// frame buffer writes and then the list's entries appended since the
// last take, and applies them to the pair in that order.
func (e *enhancedRenderer) vdp1Line(int) {
	e.takeOps(false)
}

// takeOps takes the CPU's frame buffer writes and then the list's
// entries not yet taken, in order, up to the first entry whose
// records are not final (entryRecordsFinal) unless all is set: at
// scale a distorted sprite's entry reads the record of the line
// after its last, which the walk writes a tick or two later, and an
// entry drawn before it is written differs from one drawn after it.
// The entry waits and the entries after it wait with it, so the pair
// takes them in the list's order. The CPU writes do not wait: each
// take applies the writes made since the last, so a write made while
// an entry waits lands before the entry's draw. At the end of the
// display, before the list restarts, every entry is taken as it
// stands.
func (e *enhancedRenderer) takeOps(all bool) {
	e.takeFBWrites()
	v := e.vdp1
	b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(v.vram), Records: v.lineRecords}
	var op draw.DrawOp
	end := v.opsReady(!all && e.shift != 0)
	for v.opsTaken < end {
		src := &v.ops[v.opsTaken]
		v.opsTaken++
		scaleOp(&op, src, e.shift)
		b.FB = e.vdp1FB[op.Buffer&1]
		if op.Kind == draw.OpErase {
			draw.EraseRows(b.FB, &op, 0, draw.OpLines(&op))
			continue
		}
		draw.Generate(&b, &op, 0, 1)
	}
}

// takeFBWrites takes the pages of each frame buffer the CPU wrote
// since the last take, with the bytes' marks (VDP1.takeFBPages), and
// writes each marked byte into its block of the pair half, the page
// copy params holding the scale and the frame buffer's native width
// and pixel size.
func (e *enhancedRenderer) takeFBWrites() {
	v := e.vdp1
	w := &e.fbWrites
	params := draw.PageCopyParams{ScaleShift: e.shift, FBWidth: uint32(v.fbWidth()), Is8bpp: v.is8bpp()}
	for h := range e.vdp1FB {
		var slots int
		slots, e.pages = v.takeFBPages(uint32(h), 0, w.Table[:], w.Masks[:], w.Store[:], e.pages)
		for i := range uint32(slots * compute.PageBytes) {
			draw.CopyFBPages(e.vdp1FB[h], w, &params, i)
		}
	}
}

// endDisplay takes the entries still waiting; the VDP1's list
// restarts right after the display, every entry of it taken.
func (e *enhancedRenderer) endDisplay() {
	e.takeOps(true)
}

func (e *enhancedRenderer) endFrame() {}

// syncPair does nothing: the walk keeps the CPU pair.
func (e *enhancedRenderer) syncPair(int) {}

func (e *enhancedRenderer) err() error { return nil }

// close stops the VDP1 keeping the CPU's frame buffer writes.
func (e *enhancedRenderer) close() { e.vdp1.fbWritesTaken = false }

func (e *enhancedRenderer) backend() string { return softwareBackend }

func (e *enhancedRenderer) device() string { return "" }

// framebuffer is the renderer's output frame buffer at the scaled
// geometry.
func (e *enhancedRenderer) framebuffer() []byte { return framebufferBytes(e.outputFramebuffer) }

func (e *enhancedRenderer) framebufferStride() int {
	return e.vdp2.FramebufferStride() << e.shift
}

func (e *enhancedRenderer) displayHeight() int {
	return e.vdp2.DisplayHeight() << e.shift
}

// scaleRow derives sub-row k of the row whose record is native at
// the scale shift into dst: the record copied, then every position a
// span or the composite reads carried to the scaled dots. A normal
// scroll screen's row is resolved again with the sub-row, which
// carries its source positions at the shift's more fraction bits
// (ResolveNBGLine). A rotation screen's row steps its parameters to
// the sub-row (scaleRBGRow). The gradation blurs the sub-row with its
// neighbors N sub-dots apart, one native dot. The composite's output
// frame row, width, window runs, and field bootstrap mask scale; its
// register-derived fields are the native row's. The layer buffer row
// each span writes and the composite reads is the row slot, which
// placeRow sets on the derived record. It reads the rotation state as
// the row's setup left it, so it runs at the row's setup point.
func (v *VDP2) scaleRow(dst, native *lineRecord, k int32, shift uint32) {
	*dst = *native
	n := int32(1) << shift
	vram := (*compute.VDP2VRAM)(v.vram)
	for i := 0; i < int(native.NSpans); i++ {
		switch entry, layer := spanKind(native.Spans[i]); entry {
		case entryNBGCell, entryNBGBitmap:
			p := &dst.NBG[layer]
			p.Width *= n
			p.SubRow = k
			p.MosaicH *= n
			p.Screen.ScaleShift = shift
			screen.ResolveNBGLine(vram, p)
		case entryRBGCell, entryRBGBitmap:
			v.scaleRBGRow(&dst.RBG[layer-screen.LayerRBG0], layer == screen.LayerRBG1, k, shift)
		case entryGradation:
			g := &dst.Grad
			g.Width *= n
			g.Step = n
		case entryComposite:
			c := &dst.Comp
			c.Width *= n
			c.FBRow = c.FBRow*n + k
			if c.FieldBootstrap != 0 {
				c.FieldBootstrap = uint32(n)
			}
			for r := range c.Runs.N {
				c.Runs.Runs[r].X0 *= n
				c.Runs.Runs[r].X1 *= n
			}
			v.scaleSpriteRow(&c.SpriteRow, k, shift)
		}
	}
}

// scaleSpriteRow carries the composite's VDP1 frame buffer read of a
// native row to sub-row k at the scale shift, in the pair at N times
// each axis. A direct read takes sub-row k of the native row, whose
// byte offset y*width*bytes becomes (N*y+k)*N*width*bytes, and the
// hi-res halving stays, since it halves the scaled column as it does
// the native one. A rotated read keeps its per-column step and starts
// sub-row k at N times the row's start plus k steps of the start's
// per-line advance (dXst, dYst): the frame buffer coordinate and the
// screen coordinate scale together, and the rotation's line counter
// counts display lines, so sub-row k is k over N of one advance.
func (v *VDP2) scaleSpriteRow(r *screen.SpriteRow, k int32, shift uint32) {
	switch r.Mode {
	case screen.SpriteRead16:
		r.Row = (r.Row<<shift + uint32(k)*uint32(r.Width)*2) << shift
	case screen.SpriteRead8:
		r.Row = (r.Row<<shift + uint32(k)*uint32(r.Width)) << shift
	case screen.SpriteReadRotated:
		rot := &v.frame.sprRotA
		r.BaseX = r.BaseX<<shift + int64(k)*rot.dxst
		r.BaseY = r.BaseY<<shift + int64(k)*rot.dyst
	}
	r.Width <<= shift
	r.Height <<= shift
}

// scaleOp derives into dst the entry src scaled by the shift: an
// erase's inclusive rectangle becomes its blocks' rectangle, and a normal or
// scaled sprite becomes the scaled sprite over its destination's
// blocks, [N*x0, N*(x1+1)-1] by [N*y0, N*(y1+1)-1] for the inclusive
// destination x0..x1 by y0..y1 (a normal sprite's is its character
// size at its position), with the same flags. A scaled sprite then
// resamples its character at the scaled destination's own midpoints,
// and its rows end where the native rows' records place them
// (ScaledRowEnd). For a normal sprite the midpoint sample reproduces
// the block replication: its column sample (2x+1)*CharW /
// (2*N*CharW) is x >> shift, and its row sample the same, and it
// applies the horizontal flip as the normal sprite does. A sprite's
// entry bounds are its sub-rows and sub-columns, a native row being N
// sub-rows and a native column N sub-columns. A distorted sprite, a
// polygon, a polyline, and a line have their vertices at N times, and
// their entry bounds stay native: generate admits a sub-dot by the
// native line and step whose block holds it. A drawing command's clips cover
// their native dots' blocks, the user clip's exclusive bounds scaled
// so the same strict interior holds the same blocks. The frame
// buffer's geometry scales, and every other field is copied.
func scaleOp(dst, src *draw.DrawOp, shift uint32) {
	n := int32(1) << shift
	*dst = *src
	p := &dst.Pixel
	switch src.Kind {
	case draw.OpErase:
		dst.X[0], dst.Y[0] = n*src.X[0], n*src.Y[0]
		dst.X[2], dst.Y[2] = n*(src.X[2]+1)-1, n*(src.Y[2]+1)-1
	case draw.OpNormalSprite, draw.OpScaledSprite:
		x1, y1 := src.X[2], src.Y[2]
		if src.Kind == draw.OpNormalSprite {
			x1, y1 = src.X[0]+src.CharW-1, src.Y[0]+src.CharH-1
		}
		dst.Kind = draw.OpScaledSprite
		dst.X[0], dst.Y[0] = n*src.X[0], n*src.Y[0]
		dst.X[2], dst.Y[2] = n*(x1+1)-1, n*(y1+1)-1
		dst.LineFrom <<= shift
		dst.LineTo <<= shift
		dst.LineStart <<= shift
		dst.LineEnd <<= shift
	case draw.OpDistortedSprite, draw.OpPolygon, draw.OpPolyline, draw.OpLine:
		for k := range dst.X {
			dst.X[k], dst.Y[k] = n*src.X[k], n*src.Y[k]
		}
	}
	if src.Kind != draw.OpErase {
		p.UCX1, p.UCY1 = n*(p.UCX1+1)-1, n*(p.UCY1+1)-1
		p.UCX2, p.UCY2 = n*p.UCX2, n*p.UCY2
		p.ClipX, p.ClipY = n*(p.ClipX+1)-1, n*(p.ClipY+1)-1
	}
	p.FBWidth *= n
	p.FBHeight *= n
	p.ScaleShift = shift
}

// scaleRBGRow carries p, a copy of a rotation screen's native row, to
// sub-row k at the scale shift. The width and the horizontal mosaic
// scale, and the rotation parameter window's X ranges scale as window
// edges do. Each parameter the row reads keeps its native row start
// and row coefficient address shifted left, which keeps every block
// corner exact, and advances k times the row's per-line step (VDP2
// manual Sec 6.1: Xst + dXst x Vcnt, KAst + dKAst x Vcnt) at the
// shift's more fraction bits; under double-density interlace a
// field's rows are two display lines apart, so the advance is half
// that. The step is taken from the rotation state as the row's setup
// left it, so sub-row N is the next row's sub-row 0 when that row
// does not re-read. A sub-row past the first reads its line-start
// coefficient at its own row coefficient address, as the native row
// setup reads the row's. The row's vertical count is p's Y, the native
// record's field line, since p is not yet placed at a row slot. rbg1
// selects RBG1, which reads parameter B only.
func (v *VDP2) scaleRBGRow(p *screen.RBGParams, rbg1 bool, k int32, shift uint32) {
	fs := &v.frame
	rf, needA, needB := &fs.rbg0F, fs.rbg0F.needA, fs.rbg0F.needB
	if rbg1 {
		rf, needA, needB = &fs.rbg1F, false, true
	}
	vcnt := int64(p.Y)
	n := int32(1) << shift
	p.Width *= n
	p.MosaicH *= n
	p.Screen.ScaleShift = shift
	for i := range p.Window.SX {
		p.Window.SX[i] *= n
		p.Window.EX[i] = n*(p.Window.EX[i]+1) - 1
	}
	var half uint32
	if fs.lsmd3 {
		half = 1
	}
	sets := [2]struct {
		need         bool
		t            *rotParams
		lastX, lastY int64
	}{
		{needA, &rf.paramA, rf.lastVcntXA, rf.lastVcntYA},
		{needB, &rf.paramB, rf.lastVcntXB, rf.lastVcntYB},
	}
	for i, s := range sets {
		if !s.need {
			continue
		}
		rp := &p.Param[i]
		x0, y0 := computePerLine(s.t, vcnt, s.lastX, s.lastY)
		x1, y1 := computePerLine(s.t, vcnt+1, s.lastX, s.lastY)
		rp.Xsp = rp.Xsp<<shift + (int64(k)*(x1-x0))>>half
		rp.Ysp = rp.Ysp<<shift + (int64(k)*(y1-y0))>>half
		rp.Xp <<= shift
		rp.Yp <<= shift
		if rp.CoefEn {
			rp.LineKAst = rp.LineKAst<<shift + (int64(k)*s.t.dkast)>>half
			if k != 0 {
				addr := screen.CoefAddr(rp.LineKAst>>shift, rp.KTAOF, rp.CoefOneWord)
				rp.LineCoef, rp.LineCoefMSB, rp.LineCoefLC = screen.Coefficient((*compute.VDP2VRAM)(v.vram), (*compute.CRAM)(v.cram), addr, rp.CoefOneWord, rp.CoefMode == 3, p.CRKTE)
			}
		}
	}
	screen.PrepareRBGRow(p)
}
