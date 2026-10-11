// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compute

import "embed"

// Source is this package's Go source, which the GPU renderer's compile
// stage type-checks the compute packages against. The compile stage
// lowers none of it: every function in software.go is the software
// form of an operation in Operations, which a backend gives its own
// form by name.
//
// The files are named one by one: the compile stage takes everything
// Source holds, so this file, operations.go, and the package's tests
// must stay out of it. TestEmbeddedSource holds the list against the
// directory.
//
//go:embed buffers.go roundstate.go software.go
var Source embed.FS
