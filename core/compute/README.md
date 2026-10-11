# The compute packages and their Go subset

The VDP pixel pipelines are written once, in a restricted Go subset,
and run twice: the software renderer calls them as ordinary Go
functions, and the GPU renderer's compile stage parses the same
source, lowers it to an intermediate representation, and emits the
compute shaders of the backend in use. This file is the contract
between the authors of the compute functions, the software renderer,
and the compile stage. The compile stage and its backends are
documented in internal/hostgpu/README.md.

Anything not explicitly allowed here is rejected by the compile stage
with a source-position error, and a Go test runs the same validation,
so a violation fails every test run and not only a session with the
GPU renderer enabled.


## Compilation unit

- The compute functions live in two chip subpackages, draw for the
  VDP1 and screen for the VDP2. The buffer types, the operations
  interface (Operations in operations.go: the operations a compute
  function cannot write as plain Go, each with a form per renderer),
  and the software renderer's forms of those operations (software.go)
  live in this parent package. The whole tree is subject to these
  rules.
- The subpackages may import only the parent, by name (no dot
  import), so every call of a parent helper is qualified. The parent's function
  bodies are never lowered: each is the software form of an operation
  in Operations, and the compile stage calls the GPU renderer's
  backend for the operation by the function's name. The parent
  imports nothing but embed for its source; embed.go and
  operations.go are outside the embedded set.
- Source files are ordinary .go files: go build compiles them into the
  binary and the software renderer executes them directly. Each
  package embeds its own subset source, which is what the compile
  stage parses. The embed directives name those files one by one,
  since a pattern would take the package's tests into the binary and
  hand them to the compile stage, which parses everything the embedded
  set holds. A test in this package holds each directive against its
  directory, so a new subset file cannot be left out of the set.
- The renderer names the entry points it dispatches when it hands the
  package to the compile stage; every other function, exported or
  not, is a helper emitted as a device function. Exported helpers
  exist because the software renderer calls the per-dot and per-row
  functions directly from core.
- The call graph must be acyclic: no recursion, direct or indirect
  (shading languages do not support it). The compile stage verifies
  this.
- Package-level const declarations are allowed. Package-level var is
  allowed only for lookup tables: arrays of scalars initialized by a
  literal of constants, that no function writes (Go cannot declare an
  array const). A table is used only by indexing it (and len): never
  copied, passed, or returned whole. The compile stage verifies the
  elements are constants and the table is never assigned, and lowers
  it to constant memory.
  All other package-level var is rejected; mutable state is locals,
  parameters, or buffer contents.
- No init functions, no generics, no build tags inside the packages.


## Types

Scalar types:

- bool
- int8, int16, int32, uint8, uint16, uint32
- int64, uint64 (the RBG rotation and VDP1 edge-stepping fixed-point
  math needs the width). A backend checks for 64-bit integers, and
  for whatever else its emitted form needs, at device selection, and
  a host without them uses the software renderer.
- float32. The hardware pipelines are all-integer, since their output
  must match the console bit for bit; float32 exists for enhancement
  functions that render beyond the hardware's output.

Not allowed: int, uint, uintptr (implementation-sized), float64,
complex, string, rune. An untyped or int constant (a range bound, an
index, a shift count) lowers to 32 bits, so its value must fit int32
(uint32 for uint).

Composite types:

- Fixed-size arrays of scalars or of allowed structs, at least one
  element long.
- Structs whose fields are scalars, fixed-size arrays, or other
  allowed structs. No embedding. Field order is layout: the Go struct
  and the emitted struct match field for field, and the compile stage
  computes and enforces identical offsets on both sides, since a
  stride that differs between the two would corrupt every record
  silently.
- Named types over allowed scalars are permitted.
- Buffer-visible structs, the params struct and any struct read from
  a buffer, may contain only bools, 32-bit and 64-bit scalars (int32,
  uint32, int64, uint64, float32), and arrays and structs of them. A
  bool is the byte Go stores at the offset Go gives it; a backend
  whose storage has no bool reads the word holding the byte and tests
  it against zero, so the host copies the Go struct's bytes as they
  are. 8/16-bit storage would require optional device capabilities,
  so byte-granular data rides in byte buffers. Narrow types remain
  fully usable as locals.

Buffers:

- Render function inputs and outputs are flat buffers typed as this
  package's buffer types, byte-addressed or 32-bit-word-addressed.
  A fixed-size buffer is a named array type, taken by pointer; the
  renderer converts its slices to the array pointers at the call
  boundary. A variable-length buffer is a named []uint32 or []byte,
  taken by value; core owns its allocation and growth. Every buffer's
  size is a power of two: a fixed buffer's by its constant, a
  variable buffer's checked where core allocates it and where a
  backend binds it.
- Word buffers are indexed: w[i] reads a word and w[i] = v writes
  one, compound assignments and increments included, with an int32
  or uint32 index. uint32(len(v)) is a variable-length buffer's
  length, words for a word buffer and bytes for a byte buffer (the
  length converts to int32 or uint32 where it is used); len of a
  fixed buffer is a constant. A valid index is below the
  buffer's size. An index past it is an authoring or host error: Go
  stops on it, and a backend masks the index with the size, so the
  access stays inside the buffer on a device.
- Byte buffers are read and written through the accessors in
  software.go, U8, U16BE, U32BE, PutU8, PutU16BE, and PutU32BE, never
  indexed. An access has a width and the Saturn's byte order,
  big-endian, and every byte of it is masked to the buffer's size
  individually, so an access straddling the end wraps as the
  hardware address does. The accessors are in Operations because
  their device forms differ from open-coded bytes: a target without
  byte loads and stores reads a word and extracts the byte and writes
  by masking its bytes into the word, and a 16-bit write done as two
  byte writes could interleave with another thread's write to the
  same pixel.
  A buffer type carries only the accessors a compute function uses.
  A variable-length byte buffer's accessors are value-receiver
  methods that mask with the slice's length minus one.
- Each fixed-size Saturn memory (VDP2 VRAM 512 KB, CRAM 4 KB, VDP1
  VRAM 512 KB) gets its own named buffer type. A VDP1 frame buffer
  half (VDP1FB) is a variable-length byte buffer in the hardware
  layout, VDP1FBSize (256 KB) at the native resolution. The
  renderer's intermediate buffers are variable-length: a layer buffer
  of packed pixels (Layer) and the output frame buffer (Framebuffer)
  word buffers, a rotation screen's line color buffer (LineColor) a
  byte buffer. Core sizes them per renderer, each a power of two: a
  layer or line color buffer holds the rows the renderer has between
  their setup and their render, each the row's width, and the output
  frame buffer the frame. A span's params name its row of the layer
  and line color buffers (Y), and the composite's its output frame
  row (FBRow).
- Buffers appear only as parameters and group fields, passed on
  unchanged. They cannot be held in locals or ordinary structs,
  sliced, built, grown, or reassigned.
- A buffer group is a struct, declared in any compute package, whose
  fields all hold buffers: a variable-length buffer by value, a
  fixed-size buffer or another group by pointer, or an array of
  either kind of buffer. A struct with a buffer field is a group, and
  every field must then be one of these. A compute function takes a
  group by pointer, and a path to one of its buffers (g.P.Out,
  in.LineColors[0]) is that buffer wherever a buffer may appear:
  indexed, the receiver of an accessor, the operand of len, or an
  argument; a path to a nested group is that group as an argument.
  An array of buffers is indexed by a constant, except that a word
  of it may be read by a variable index as the whole value of an
  assignment (px := in.Layers[p.LayerBuf[i]][pi]), which the compile
  stage lowers to a switch over the array's buffers. The compile
  stage binds every buffer a group holds as its own buffer, depth
  first in field order, so the IR and the backends see ordinary
  buffers and the host binds them in that order. The round state of
  the VDP1 draw, draw's Buffers, the page staging sets, and the
  composite's inputs are groups.
- No workgroup-shared memory is exposed; ordering between dispatches
  is the host's job (encoder barriers), and ordering within a
  dispatch is by construction (a thread owns what it writes) or, in
  a group entry, by the one group primitive: NextPhase(), the point
  where the group's next phase begins, after which every buffer
  store the group's threads made before it is visible to every
  thread. It is called only as a statement directly in the body of a
  group entry, outside any if, switch, or loop (validated), so every
  thread of the group reaches it.
- Three shared-word operations are exposed through Operations on
  variable-length word buffers, each masking its index with the
  region's length, each on the round state region whose role needs
  it.
  Raise, on the order stamps, raises a word to a value and returns
  nothing: the threads raising one word leave it at the greatest of
  their values in whatever order the backend runs them, so a compute
  function uses it to decide which of several dots on a pixel came
  last, by a key that carries the order. Add, on the fragment pool,
  adds to a word and returns the word's prior value: the threads
  adding to one word each take a distinct prior and leave the word
  at their sum, so a compute function uses it to reserve a range of
  a stream, never to order its threads. Swap, on the fragment list
  heads, stores a value into a word and returns the prior: the
  threads swapping into one word each take what the one before it
  stored, so a compute function links itself at the head of a list.
  A region type carries only the operation its role needs, so the
  wrong operation on a region is a compile error. A prior value is
  used for addressing or stored as a link and is not fed to another
  Add, since a device compiler may coalesce the adds of a group of
  lanes into one, and the conformance rules test the addressing and
  linking forms alone.
  A further primitive is added in the form a compute function needs
  when one does.
- Operations (operations.go, outside the embedded set) is the list of
  operations a renderer implements for the compute functions: the
  byte buffer accessors, the word buffer index and store, the length
  of a variable-length buffer, min and max, the three shared-word
  operations, and NextPhase, the operations a compute function cannot
  write as plain Go. Their software forms are the functions of
  software.go, which the software renderer runs, one thread per
  dispatch, and which are the single-thread reference the conformance
  rules hold every backend to. The compile stage never compiles them:
  it calls the GPU renderer's backend, which implements Operations over
  its own operands, for each operation it meets, an atomic for the
  shared-word operations and a group barrier for NextPhase. A test of
  this package holds every function of the embedded source to an
  operation in Operations: its name, a buffer receiver exactly when the
  operation acts on a buffer, and its operand count. The compile
  stage holds each call of an operation to the operand count and
  result the interface gives it, and a backend missing an operation
  does not build.

Pointers:

- Allowed only as method receivers and function parameters referring
  to a local struct or scalar (lowered to references in the thread's
  own storage). This covers the stepper pattern (a helper mutating
  its receiver per pixel).
- Address-of appears only as a call argument, and its operand is a
  struct or scalar held in a local or a parameter, or a field or
  element path into one (&cands[i], &p.Window). The receiver of a
  pointer-receiver call, whose address the call takes, follows the
  same rule, so a table element is never a receiver. A pointer parameter's
  target is a value of the thread's own or the thread's params row,
  which is never written, so such a reference never reaches storage
  another thread writes.
- No pointer arithmetic, no storing pointers in structs, arrays, or
  locals (a local aliasing part of the params struct is the excluded
  case: a target may have no pointers; select by index instead), no
  reassigning a pointer parameter, no nil, no address-of buffer
  contents, no returning pointers.
- A call may pass two references into the same storage only to a
  callee that writes through neither. A reference may be a copy on a
  device: a target may implement a reference parameter by copying the
  value in at the call and back at the return, so two overlapping
  references are two private copies there and one object in Go, and
  the difference shows when the callee writes one and reads the
  other. The compile stage holds the rule: references overlap when
  one's path from the root variable is a prefix of the other's, with
  an index that is not a constant matching any element, and a callee
  writes through a parameter when it or a callee it passes the
  pointer, or a path into it, to assigns through it. Reading both is allowed
  (ranksAbove(&cands[i], &cands[j])), and so are distinct constant
  indices and distinct fields.
- A statement may not read, outside a call, storage that the call
  writes: a path overlapping a reference argument the callee writes
  through (`i + bump(&i)`, `a[i] += bump(&i)`), or a word of a buffer
  the call takes (`w[i] + put(w, i)`). Go leaves the order of a call
  and a plain read unspecified, and the software renderer, built by
  gc, runs the call first while a device may read first. A plain
  assignment target is not a read, and neither is an operand of the
  call itself. Overlap is by path, as for references.

Slices, maps, channels, interfaces, function values: not allowed.


## Declarations and statements

Allowed:

- const, var with explicit or inferred (fixed-width) type, short
  variable declarations, multiple assignment.
- Assignment, compound assignment (+=, |=, ...), increment, decrement.
- if / else.
- switch on an integer or bool tag with constant cases and default.
  No tagless switch, no fallthrough, no type switches.
- for in all three header forms (init; cond; post), condition-only,
  and infinite-with-break, and range over an integer (for i := range
  n, for range n), whose key takes n's type, so n is a fixed-width
  integer wherever the key is used. break and continue, unlabeled
  only, and break always leaves the innermost loop: a break directly
  inside a switch case is rejected, since in Go it would leave the
  switch. No range over arrays or anything but an integer, no labels,
  no goto.
- return, including multiple return values and named results with
  bare return. Multiple results are lowered to out parameters per
  backend.
- Calls to functions within the compute package tree and the software
  forms of the renderer operations. Method sets use value or pointer
  receivers; methods lower to functions with an explicit receiver
  argument.
- Struct and array literals (keyed or positional), local composite
  variables, copying structs and arrays by value.

Not allowed: defer, go, select, closures and anonymous functions,
variadic functions, panic/recover, type assertions, new, make,
append, copy, builtins other than min and max on int32, int64, and
uint32 operands (any number of them, lowered to each backend's native
form), len/cap on fixed-size arrays (where they are compile-time
constants), and len of a variable-length buffer converted to int32 or
uint32.


## Semantics (defined by the IR, honored by every emitter)

These are Go's semantics. Where a target language differs, the
emitter compensates; the conformance functions verify each rule per
backend per operation.

- Integer arithmetic wraps modulo 2^width at the operand's declared
  width. Emitters computing in 32-bit registers mask or sign-extend
  at assignments, conversions, comparisons, and narrowing points so
  observable values always match the declared width.
- Signed right shift is arithmetic, unsigned is logical.
- Shift counts must be less than the operand width. This is an
  authoring rule, checked where the count is constant; for variable
  counts the software path is the arbiter (Go defines the result,
  GPU targets mask the count to the width, so a variable count that
  can reach the width is an authoring bug).
- Integer division and remainder truncate toward zero, as Go's do,
  and every emitter produces that result. Division by zero is an authoring bug: the software
  path panics, GPU behavior is undefined. Divisors must be guarded or
  provably nonzero. A 32-bit or 64-bit signed minimum divided by minus
  one is the backend's native behavior, which the targets leave
  undefined, and is not guaranteed to match Go, which wraps the
  quotient to the minimum and the remainder to zero. Narrow widths
  are not affected: they divide in a 32-bit register and the Mask
  re-canonicalizes the result.
- Conversions are explicit only. Narrowing truncates, widening
  zero-extends (unsigned) or sign-extends (signed), exactly as in Go.
  No implicit conversions anywhere, including untyped constants,
  which must fit the target type.
- Evaluation order is Go's left-to-right. Emitters introduce
  temporaries where the target's order is unspecified.
- bool is not an integer; no arithmetic on bool, no implicit tests.
- float32 is IEEE 754 binary32 with no fast-math relaxations;
  emitters set the precise-math options on their pipelines.


## Entry points and execution

- An entry point's parameter list is, in order: buffers and buffer
  groups, then at most
  one params struct passed by pointer and never written through (the
  captured register state and per-dispatch constants), optionally one
  row-state struct passed by pointer that the function may update
  (explicit state carried across the software schedule's span calls
  on a row, such as a pattern name cache or a rasterizer's position;
  it starts fresh per row and per GPU thread), then scalar thread
  coordinates: two for a span (x0, x1), one for an index, or three
  for a group entry (the thread's index in its group, the group's
  size, and the group's index, whose params row is the group's). The
  params pointer exists for the software schedule: it calls a span
  for every few dots, and a by-value struct of a few hundred bytes
  copied per call is a frame-time cost. The GPU wrapper passes the
  thread's row of the params buffer. The order
  of the two structs is the whole of what tells them apart, so the
  compile stage holds it by the one difference the source shows: the
  params row is never written through, directly or by a function it
  or a path into it is passed to. Writing it would reach the caller's struct in
  software and the thread's params row on a device, and an entry
  written with the two the other way round would run in software and
  read a zeroed params row on a device.
- Thread identity arrives through those trailing coordinate
  parameters. On the GPU the generated shader wrapper maps the global
  invocation id onto them; in software the renderer or a test calls
  the entry point in an ordinary loop. There are no thread-id
  intrinsics, which is what keeps the compute functions directly
  callable and testable.
- On the software schedule, entry points are called per row or per
  span, never per pixel: the x loop lives inside the compute
  function, and its prologue loads params fields into locals once per
  row. A group entry's software execution is one thread per group,
  thread 0 of 1, which runs the group's phases in order. A group
  entry is written so its result is the same for any group size, and
  the number of threads a group gets is the backend's, sized from the
  group's work. The barrier (NextPhase) orders one workgroup, so an
  entry that uses it is dispatched one workgroup across.
- A span entry's segment is the unit one thread takes whole, set by
  the renderer for the entry (a cell, a whole row, or one dot), and
  the dots a thread takes past the unit are the backend's. A span
  entry is written so its result is the same for any segment.
- The grid's shape is the renderer's and the workgroup size the
  backend's; compute functions are written against coordinates, not
  group layout.
- A compute function must bound every loop by values derived from its
  parameters and must clamp counts read from buffer contents before
  looping on them, since a count taken from garbage would otherwise
  loop for billions of iterations. The compile stage cannot prove
  boundedness; this is an authoring rule enforced in review, with
  min as the clamp.
- A byte buffer accessor masks its address to the buffer's size in
  both implementations, matching the address masking of the hardware
  memory. A word buffer index past the buffer is an error: Go stops
  on it, and a backend masks it so the access stays inside the
  buffer.


## Scale

The compute functions render at a scale N = 1 << ScaleShift on both
axes, chosen by the renderer that calls them: ScaleShift 0 is the
native resolution, and 1 to 4 are the enhanced renderers' 2x, 4x, 8x,
and 16x. The same functions run at every scale; the scale is a parameter,
and at its native value every function computes exactly what it
computed before the parameter existed. The native output is held to
the console bit for bit, so a change to a compute function must keep
the native path's arithmetic unchanged at ScaleShift 0.

- The parameters that carry the scale: draw.PixelParams.ScaleShift,
  with FBWidth and FBHeight the scaled frame buffer's;
  screen.ScreenParams.ScaleShift, which the NBG and RBG params hold;
  screen.NBGParams.SubRow, the sub-row of the native row (0 native);
  screen.GradationParams.Step, the blur's dot distance (1 native, N at
  scale); screen.CompositeParams.FieldBootstrap, the other field's row
  xor mask (0 off, 1 native, N at scale); and draw.PageCopyParams'
  ScaleShift, FBWidth, and Is8bpp, which place a CPU-written byte's
  N by N block in the scaled frame buffer.
- A scaled position is the native fixed-point value carried at
  ScaleShift more fraction bits, never a divided step: the NBG source
  positions at 8 + ScaleShift bits, the rotation screen's row start,
  center, and coefficient address (Xsp, Ysp, Xp, Yp, LineKAst, and a
  coefficient's replacement of Xp) at 10 + ScaleShift, the rotated
  sprite read's column 0 coordinate (SpriteRow.BaseX and BaseY) at
  10 + ScaleShift with its per-column step native, and the VDP1
  texture coordinates and Gouraud steppers at 16 + ScaleShift. The
  per-dot steps stay native, since the thread coordinate is the scaled
  column. A value produced at one width and read at another moves a
  block corner, so the width a params field is carried at is part of
  its definition.
- Geometry refines to the sub-dot and content keeps its native
  granularity: texel selection, the mesh and interlace parities, the
  double-interlace row, mosaic, and the per-line tables are taken on
  the native dot, the coordinate shifted right by ScaleShift.
- The coordinates admitted at scale reach draw.nativeCoordLimit
  shifted left by ScaleShift, so a product of a coordinate delta and
  a step count is taken in int64, as the walk's position functions
  take it.
- In the draw package a normal sprite is drawn as the scaled sprite
  over its blocks: each sub-column samples the native column whose
  block holds it, while its Gouraud is interpolated over the scaled
  width, so a block's dots share a texel and not a shade; a scaled
  sprite and a distorted sprite resample their character at the
  scaled destination; a connecting line, a line, and a polyline leg
  are the native walk on the scaled vertices, each dot claiming the N
  by N square on its + sides, a later dot of the line replacing an
  earlier one's claim, and the next line's dots replacing this line's
  when the two share a major axis (square.go). A line ends where the native walk's
  record ended it, never on a fresh end code count, since the finer
  sampling reads columns the native line skipped: a sprite row at
  ScaledRowEnd, a connecting line at the native step whose block
  holds the record. A distorted sprite's line at scale also reads the
  record of the line after it, so an entry is drawn once that record
  is final.
- A compute function sees one row of the scaled width. The
  derivation of a native row's N sub-rows (the sub-row's source
  positions, rotation parameters, and output row) is the renderer's,
  in core.


## Verification

- The software execution of the compute packages is the reference
  semantics. The Go test suite tests the compute functions directly.
- Conformance functions, one per operation class per rule above, are
  emitted and run on every backend and compared against the software
  results bit-exactly. They contain no VDP logic and lock down the
  emitters independently of renderer changes. They live in the
  conformance subpackage under the same subset rules. Each backend's
  tests run them on that platform's hardware device and skip where
  there is none.
- The renderer's device tests in core drive the GPU renderer through
  its scenes on the platform's device and hold every frame
  bit-identical to the software renderer at native resolution, and
  the enhanced GPU renderer to the enhanced software renderer at
  every scale. The enhanced renderers at scale 1 equal the accurate
  ones byte for byte.
