package tui

import pbsubstreams "github.com/streamingfast/substreams/pb/sf/substreams/v1"

type msg int

const (
	Connecting msg = iota
	Connected

	Quit
)

func (ui *TUI) Connecting() {
	ui.prog.Send(Connecting)
}
func (ui *TUI) Connected() {
	ui.prog.Send(Connected)
}
func (ui *TUI) SetRequest(req *pbsubstreams.Request) {
	ui.prog.Send(req)
}

type BlockMessage string
