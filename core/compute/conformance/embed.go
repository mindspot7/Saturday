// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package conformance

import "embed"

// Source is this package's Go source for the GPU renderer's compile
// stage.
//
// The files are named one by one: the compile stage takes everything
// Source holds, so this file and the package's tests must stay out of
// it. TestEmbeddedSource holds the list against the directory.
//
//go:embed rules.go
var Source embed.FS
