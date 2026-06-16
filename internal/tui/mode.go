package tui

type mode int

const (
	modeNav mode = iota
	modeCommand
	modeConfirm
	modeRunning
)
