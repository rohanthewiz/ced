// =============================================================================
// File: internal/version/version.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package version exposes ced's release version. Keep this file
// tiny — it's the single source of truth a release bumps by hand (with
// cats-plugin.toml's version), and a one-line diff is trivial to review.
package version

// Version is the ced release version, displayed in the menu footer.
// Bump this constant on each release.
const Version = "0.6.2"
