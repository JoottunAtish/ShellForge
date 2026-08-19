//go:build windows

package wsl

// Resize is a no-op on Windows for now. ConPTY resize support lands with
// #68's real terminal integration; until then a resize during a full
// screen program such as vim still corrupts the display, which is a known
// gap, not an oversight.
//
// TODO(v0.2): real ConPTY resize lands with #68.
func (p *wslPTY) Resize(rows, cols uint16) error {
	return nil
}
