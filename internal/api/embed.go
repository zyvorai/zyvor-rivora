// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package api

import "embed"

// Built Rivora console (web/ → vite outDir). Run `npm run build` in web/
// before compiling rivorad so this tree is populated.
//
//go:embed all:ui
var uiContent embed.FS
