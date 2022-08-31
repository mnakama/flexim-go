package irc_style

var colorCodes = []string{
	"white", // 0
	"black",
	"blue",
	"green",
	"red",
	"brown",
	"magenta",
	"orange",
	"yellow",
	"light green",
	"cyan",
	"light cyan",
	"light blue",
	"ping",
	"grey",
	"light grey", // 15

	"#470000", // 16
	"#472100",
	"#474700",
	"#324700",
	"#004700",
	"#00472c",
	"#004747",
	"#002747",
	"#000047",
	"#2e0047",
	"#470047",
	"#47002a", // 27

	"#740000", // 28
	"#743a00",
	"#747400",
	"#517400",
	"#007400",
	"#007449",
	"#007474",
	"#004074",
	"#000074",
	"#4b0074",
	"#740074",
	"#740045", // 39

	"#b50000", // 40
	"#b56300",
	"#b5b500",
	"#7db500",
	"#00b500",
	"#00b571",
	"#00b5b5",
	"#0063b5",
	"#0000b5",
	"#7500b5",
	"#b500b5",
	"#b5006b", // 51

	"#ff0000", // 52
	"#ff8c00",
	"#ffff00",
	"#b2ff00",
	"#00ff00",
	"#00ffa8",
	"#00ffff",
	"#008cff",
	"#0000ff",
	"#a500ff",
	"#ff00ff",
	"#ff0098", // 63

	"#ff5959", // 64
	"#ffb459",
	"#ffff71",
	"#cfff60",
	"#6fff6f",
	"#65ffc9",
	"#6dffff",
	"#59b4ff",
	"#5959ff",
	"#c459ff",
	"#ff66ff",
	"#ff59bc", // 75

	"#ff9c9c", // 76
	"#ffd39c",
	"#ffff9c",
	"#e2ff9c",
	"#9cff9c",
	"#9cffdb",
	"#9cffff",
	"#9cd3ff",
	"#9c9cff",
	"#dc9cff",
	"#ff9cff",
	"#ff94d3", // 87

	"#000000", // 88
	"#131313",
	"#282828",
	"#363636",
	"#4d4d4d",
	"#656565",
	"#818181",
	"#9f9f9f",
	"#bcbcbc",
	"#e2e2e2",
	"#ffffff", // 98
}

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

func colorVal(color int) string {
	if color < 0 || (color > len(colorCodes)-1) {
		return "grey"
	}
	return colorCodes[color]
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
