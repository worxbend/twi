// Package render converts normalized chat messages and asset state into
// terminal-safe rows.
//
// It owns semantic fragments, width-aware wrapping, color decisions, fixed
// image placeholders, bounded image preparation, and terminal image rendering
// boundaries. Rendering must preserve stable text fallbacks so late or failed
// image work does not reflow the chat viewport.
package render
