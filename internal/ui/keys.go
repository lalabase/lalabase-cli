package ui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

type Mode uint8

const (
	Navigate Mode = iota
	TextEntry
)

type Action uint8

const (
	Ignore Action = iota
	Quit
	Interrupt
	Left
	Right
	OpenList
	Back
	InsertText
	Submit
	Cancel
)

type keyAction struct {
	Kind Action
	Text string
}

func routeKey(mode Mode, key tea.KeyPressMsg) keyAction {
	name := key.String()
	if name == "ctrl+c" {
		return keyAction{Kind: Interrupt}
	}
	if mode == TextEntry {
		switch name {
		case "enter":
			return keyAction{Kind: Submit}
		case "esc":
			return keyAction{Kind: Cancel}
		}
		text := key.Text
		if text == "" && key.Code <= unicode.MaxRune && unicode.IsPrint(key.Code) && key.Mod & ^tea.ModShift == 0 {
			text = string(key.Code)
		}
		if text != "" && key.Mod & ^tea.ModShift == 0 {
			for _, r := range text {
				if !unicode.IsPrint(r) {
					return keyAction{}
				}
			}
			return keyAction{Kind: InsertText, Text: text}
		}
		return keyAction{}
	}
	switch name {
	case "q":
		return keyAction{Kind: Quit}
	case "left":
		return keyAction{Kind: Left}
	case "right":
		return keyAction{Kind: Right}
	case "enter":
		return keyAction{Kind: OpenList}
	case "esc":
		return keyAction{Kind: Back}
	}
	return keyAction{}
}
