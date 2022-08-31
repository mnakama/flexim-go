package irc_style

func getModeTag(mode rune) string {
	switch mode {
	case '\x02':
		return "b"
	case '\x1d':
		return "i"
	case '\x1e':
		return "s"
	case '\x1f':
		return "u"
	case '\x11':
		return "tt"
	case '\x03':
		return "span"
	default:
		return ""
	}
}

func colorVal(color int) (ret string) {
	switch color {
	case 0:
		return "white"
	case 1:
		return "black"
	case 2:
		return "blue"
	case 3:
		return "green"
	case 4:
		return "red"
	case 5:
		return "brown"
	case 6:
		return "magenta"
	case 7:
		return "orange"
	case 8:
		return "yellow"
	case 9:
		return "light green"
	case 10:
		return "cyan"
	case 11:
		return "light cyan"
	case 12:
		return "light blue"
	case 13:
		return "pink"
	case 14:
		return "grey"
	case 15:
		return "light grey"
	}

	return "white"
}

func setColorTag(fg, bg int) (ret string) {
	fgStr := ""
	bgStr := ""
	if fg >= 0 {
		fgStr = `fgcolor="` + colorVal(fg) + `"`
	}
	if bg >= 0 {
		bgStr = `bgcolor="` + colorVal(bg) + `"`
	}
	ret = `<span ` + fgStr + " " + bgStr + `>`
	return
}

func setTag(tag string) (ret string) {
	ret = "<" + tag + ">"

	return
}

func unsetTag(tag string) (ret string) {
	ret = "</" + tag + ">"

	return
}

func IRCToPango(msg string) (newMsg string) {
	var (
		modeStatus          = make(map[rune]bool)
		modeStack    []rune = make([]rune, 0, 6)
		redoStack    []rune = make([]rune, 0, 6)
		colorState   int    = 0
		colorDigits  int    = 0
		fgColor      int    = -1
		fgColorReset bool   = false
		bgColor      int    = -1
		bgColorReset bool   = false
	)

	setMode := func(mode rune) {
		if modeStatus[mode] {
			return
		}
		modeStatus[mode] = true
		newMsg += setTag(getModeTag(mode))
		modeStack = append(modeStack, mode)
	}

	unsetMode := func(mode rune) {
		if !modeStatus[mode] {
			return
		}
		modeStatus[mode] = false
		for {
			undoRune := modeStack[len(modeStack)-1]
			newMsg += unsetTag(getModeTag(undoRune))
			modeStack = modeStack[:len(modeStack)-1]
			if undoRune != mode {
				redoStack = append(redoStack, undoRune)
			} else {
				for len(redoStack) > 0 {
					redoRune := redoStack[len(redoStack)-1]
					if redoRune == 0x03 {
						newMsg += setColorTag(fgColor, bgColor)
					} else {
						newMsg += setTag(getModeTag(redoRune))
					}
					modeStack = append(modeStack, redoRune)
					redoStack = redoStack[:len(redoStack)-1]
				}
				break
			}
		}
	}

	unsetAllModes := func() {
		for len(modeStack) > 0 {
			newMsg += unsetTag(getModeTag(modeStack[len(modeStack)-1]))
			modeStack = modeStack[:len(modeStack)-1]
		}
	}

	toggleMode := func(mode rune) {
		if !modeStatus[mode] {
			setMode(mode)
		} else {
			unsetMode(mode)
		}
	}

	setColor := func() {
		unsetMode(0x03)

		newMsg += setColorTag(fgColor, bgColor)

		modeStatus[0x03] = true
		modeStack = append(modeStack, '\x03')
	}

	unsetColor := func() {
		fgColor = -1
		bgColor = -1
		unsetMode(0x03)
	}

	for _, rune := range msg {
		if colorState == 1 { // foreground
			if rune == ',' {
				if fgColorReset {
					colorState = 0
					unsetColor()
					newMsg += string(rune)
					continue
				}

				colorDigits = 0
				colorState = 2
				continue
			}
			if colorDigits >= 2 || rune < '0' || rune > '9' {
				// invalid color data
				if fgColorReset {
					colorDigits = 0
					colorState = 0
					unsetColor()
					newMsg += string(rune)
					continue
				}

				colorDigits = 0
				colorState = 0
				setColor()
				newMsg += string(rune)
				continue
			}
			if fgColor < 0 || fgColorReset {
				fgColor = 0
				fgColorReset = false
			}
			fgColor *= 10
			fgColor += int(rune - '0')
			colorDigits++

		} else if colorState == 2 {
			if colorDigits >= 2 || rune < '0' || rune > '9' {
				// invalid color data
				colorDigits = 0
				colorState = 0
				setColor()
				newMsg += string(rune)
				continue
			}
			if bgColor < 0 || bgColorReset {
				bgColor = 0
				bgColorReset = false
			}
			bgColor *= 10
			bgColor += int(rune - '0')
			colorDigits++

		} else if rune == '\x0f' { // erase all formatting
			unsetAllModes()
		} else if rune == '\x03' {
			colorState = 1
			colorDigits = 0
			fgColorReset = true
			bgColorReset = true
		} else if rune < '\x20' {
			tag := getModeTag(rune)
			if tag == "" {
				newMsg += string(rune)
				continue
			}

			toggleMode(rune)
		} else {
			newMsg += string(rune)
		}
	}

	// clear formatting
	unsetAllModes()

	return
}
