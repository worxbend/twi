// Package storage owns persistent cache and credential storage boundaries.
//
// Asset cache records use deterministic, URL-free paths for metadata,
// downloaded bytes, and prepared renderer bytes. Credential persistence is
// supported only on Go unix builds through a restrictive credential file with
// exact private permissions, symlink rejection, no-follow opens, and atomic
// replacement; non-Unix saved credentials remain unsupported.
package storage
