// Package operations provides the execution logic of each go-comic-kit operation
// (outline, chapter script, design sheet, panel image, page composition). Every
// operation follows the idempotent contract of taking a MangaState and returning the
// updated MangaState (see the ports package).
package operations
