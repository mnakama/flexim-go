package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/yaml.v2"
)

const defaultPeerNick = "them" // Used if we do not have chat partner's nick

// User config variables
var config struct {
	Nickname string
	Address  string
}

// globals
var (
	sock          proto.Socket
	sentFirstLine bool
	peerNick      string
	peerName      = flag.String("to", "", "Name of chat partner")
	unixAddress   = flag.String("unix", "", "Unix socket address to connect")

	chat       *gtk.TextView
	chatBuffer *gtk.TextBuffer
	chatScroll *gtk.ScrolledWindow
	entry      *gtk.Entry
)

func timestamp(t time.Time) string {
	return t.Format("15:04")
}

func cb_Message(msg *proto.Message) {
	// called when we receive a message

	if msg.From != "" {
		peerNick = msg.From
	}

	var msgTime time.Time

	if msg.Date != 0 {
		msgTime = time.Unix(msg.Date, 0)
	} else {
		msgTime = time.Now()
	}

	glib.IdleAdd(func() bool {
		appendMsg(msgTime, msg.From, msg.Msg)
		return false
	})
}

func cb_Text(txt string) {
	fmt.Println(txt)
	glib.IdleAdd(func() bool {
		appendText(txt)
		return false
	})
}

func cb_Disconnect() {
	// TODO: disable message sending
}

func cb_Auth(auth *proto.Auth) {
	// TODO: something
	fmt.Printf("Received Auth packet for some reason? %s\n", auth)
}

func cb_Status(status *proto.Status) {
	txt := fmt.Sprintf("%d: %s", status.Status, status.Payload)

	glib.IdleAdd(func() bool {
		appendText(txt)
		return false
	})
}

func cb_Roster(roster *proto.Roster) {
	txt := ""
	for _, user := range *roster {
		txt += fmt.Sprintf("User: %s %x\n", user.Aliases, user.Key)
	}

	glib.IdleAdd(func() bool {
		appendText(txt)
		return false
	})
}

func cb_Command(cmd *proto.Command) {
	switch cmd.Cmd {
	case "NICK":
		if cmd.Payload != nil {
			oldNick := peerNick
			peerNick = cmd.Payload[0]
			glib.IdleAdd(func() bool {
				appendText(fmt.Sprintf("%s is now known as %s", oldNick, cmd.Payload))
				return false
			})
		} else {
			peerNick = defaultPeerNick
		}
	default:
		glib.IdleAdd(func() bool {
			appendMsg(time.Now(), peerNick, cmd.Cmd)
			return false
		})
	}

}

func cb_RoomMemberJoin(member *proto.RoomMemberJoin) {
	glib.IdleAdd(func() bool {
		appendMarkup(fmt.Sprintf("<span color=\"brown\">%s joined the channel</span>", *member))
		return false
	})
}

func cb_RoomMemberPart(msg *proto.RoomMemberPart) {
	glib.IdleAdd(func() bool {
		var desc string
		if msg.HasQuit {
			desc = "has quit"
		} else {
			desc = "left the channel"
		}

		appendMarkup(fmt.Sprintf("<span color=\"brown\">%s %s (%s)</span>",
			msg.Member, desc, msg.Msg))

		return false
	})
}

func scrollToBottom() {
	adj := chatScroll.GetVAdjustment()
	page := adj.GetPageSize()
	upper := adj.GetUpper()
	bottom := upper - page

	adj.SetValue(bottom)

	chatScroll.SetVAdjustment(adj)
}

func appendText(text string) {
	end := chatBuffer.GetEndIter()

	var str string
	if !sentFirstLine {
		sentFirstLine = true
		str = text
	} else {
		str = "\n" + text
	}

	chatBuffer.Insert(end, str)
}

func appendMarkup(text string) {
	end := chatBuffer.GetEndIter()

	var str string
	if !sentFirstLine {
		sentFirstLine = true
		str = text
	} else {
		str = "\n" + text
	}

	chatBuffer.InsertMarkup(end, str)
}

func escapeAngles(msg string) string {
	for {
		if idx := strings.Index(msg, "<"); idx > -1 {
			msg = msg[:idx] + "&lt;" + msg[idx+1:]
		} else {
			break
		}
	}
	for {
		if idx := strings.Index(msg, ">"); idx > -1 {
			msg = msg[:idx] + "&gt;" + msg[idx+1:]
		} else {
			break
		}
	}

	return msg
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
		fgStr = `fgcolor="`+colorVal(fg)+`"`
	}
	if bg >= 0 {
		bgStr = `bgcolor="`+colorVal(bg)+`"`
	}
	ret = `<span `+fgStr+" "+bgStr+`>`
	return
}

func setTag(tag string) (ret string) {
	ret = "<"+tag+">"

	return
}

func unsetTag(tag string) (ret string) {
	ret = "</"+tag+">"

	return
}

func appendMsg(t time.Time, who string, msg string) {
	end := chatBuffer.GetEndIter()

	timestampText := "<tt>"+timestamp(t)+"</tt> "

	if !sentFirstLine {
		sentFirstLine = true
	} else {
		timestampText = "\n" + timestampText
	}

	chatBuffer.InsertMarkup(end, timestampText)

	if idx := strings.Index(who, "!"); idx > -1 {
		who = who[:idx]
	}

	end = chatBuffer.GetEndIter()
	chatBuffer.InsertMarkup(end, "<b>"+who+"</b>  ")

	msg = escapeAngles(msg)

	var (
		modeStatus = make(map[rune]bool)
		modeStack []rune = make([]rune, 0, 5)
		redoStack []rune = make([]rune, 0, 5)
		colorState int = 0
		fgColor int = -1
		fgColorReset bool = false
		bgColor int = -1
		bgColorReset bool = false
	)


	newMsg := ""

	setMode := func(mode rune) {
		if modeStatus[mode] {return}
		modeStatus[mode] = true
		newMsg += setTag(getModeTag(mode))
		modeStack = append(modeStack, mode)
	}

	unsetMode := func(mode rune) {
		if !modeStatus[mode] {return}
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

				colorState = 2
				continue
			}
			if rune < '0' || rune > '9' {
				// invalid color data
				if fgColorReset {
					colorState = 0
					unsetColor()
					newMsg += string(rune)
					continue
				}

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

		} else if colorState == 2 {
			if rune < '0' || rune > '9' {
				// invalid color data
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

		} else if rune == '\x0f' { // erase all formatting
			unsetAllModes()
		} else if rune == '\x03' {
			colorState = 1
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

	end = chatBuffer.GetEndIter()
	chatBuffer.InsertMarkup(end, newMsg)
}

func sendEntry() {
	entryText, err := entry.GetText()
	if err != nil {
		log.Panic(err)
	}

	if entryText == "" { // TODO: check if connected
		return
	}

	if entryText[0] == '/' {
		appendText(entryText)

		entry.SetText("")
		c := strings.SplitN(entryText[1:], " ", 2)

		cmd := proto.Command{
			Cmd: c[0],
		}

		if len(c) > 1 {
			cmd.Payload = []string{c[1]}
		}

		switch strings.ToLower(cmd.Cmd) {
		case "nick":
			cmd.Cmd = "NICK"
			err := sock.SendCommand(&cmd)
			if err != nil {
				log.Panic(err)
			}
		case "bye":
			cmd.Cmd = "BYE "
			err := sock.SendCommand(&cmd)
			if err != nil {
				log.Panic(err)
			}
		case "msgpack":
			sock.SetSendMode(proto.ModeMsgpack)
		case "text":
			sock.SetSendMode(proto.ModeText)
		case "roster":
			cmd.Cmd = "ROSTER"
			sock.SendCommand(&cmd)

		// IRC commands
		case "query":
			cmd.Cmd = "QUERY"
			sock.SendCommand(&cmd)
		case "q":
			cmd.Cmd = "QUERY"
			sock.SendCommand(&cmd)
		case "msg":
			cmd.Cmd = "PRIVMSG"
			if len(cmd.Payload) <= 0 {
				appendText("Usage: /msg {target} {message}")
				return
			}
			params := strings.SplitN(cmd.Payload[0], " ", 2)
			cmd.Payload = params
			sock.SendCommand(&cmd)
		case "whois":
			cmd.Cmd = "WHOIS"
			sock.SendCommand(&cmd)
		case "ping":
			cmd.Cmd = "PING"
			sock.SendCommand(&cmd)
		case "join":
			cmd.Cmd = "JOIN"
			sock.SendCommand(&cmd)
		case "part":
			cmd.Cmd = "PART"
			sock.SendCommand(&cmd)
		case "quit":
			cmd.Cmd = "QUIT"
			sock.SendCommand(&cmd)
		case "raw":
			cmd.Cmd = "RAW"
			sock.SendCommand(&cmd)
		default:
			appendText("Unknown Command")
		}
	} else {
		msg := proto.Message{
			To:    *peerName,
			From:  config.Nickname,
			Flags: []string{},
			Date:  time.Now().Unix(),
			Msg:   entryText,
		}

		err := sock.SendMessage(&msg)
		if err != nil {
			log.Print(err)
			appendText(err.Error())
		} else {
			appendMsg(time.Now(), config.Nickname, entryText)
			entry.SetText("")
		}
	}
}

func SetProp(name string, value interface{}) {
	if _, ok := value.(glib.Object); ok {
		log.Print("it's an Object")
	} else {
		log.Print("it's not an Object")
	}
}

func chatWindow() {
	gtk.Init(nil)

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Panic(err)
	}

	win.SetTitle(*peerName)
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	win.SetDefaultSize(400, 600)

	box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	if err != nil {
		log.Panic(err)
	}
	win.Add(box)

	chatScroll, err = gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		log.Panic(err)
	}

	chat, err = gtk.TextViewNew()
	if err != nil {
		log.Panic(err)
	}
	chat.SetEditable(false)
	chat.SetWrapMode(gtk.WRAP_WORD)
	chat.Connect("size-allocate", scrollToBottom)
	chatScroll.Add(chat)

	chatBuffer, err = chat.GetBuffer()
	if err != nil {
		log.Panic(err)
	}

	entry, err = gtk.EntryNew()
	if err != nil {
		log.Panic(err)
	}

	/*nickTag, err := gtk.TextTagNew("nick")
	if err != nil {
		log.Panic(err)
	}

	var blueRGBA *gdk.RGBA
	blueRGBA = gdk.NewRGBA(0.3, 0.3, 1, 1)
	log.Printf("blue: %+V", blueRGBA)*/

	/*prop, err := nickTag.GetProperty("foreground-rgba")
	if err != nil {
		log.Panic(err)
	}
	log.Printf("prop: %+V", prop)*/

	/*if err := nickTag.SetProperty("foreground-rgba", blueRGBA); err != nil {
		log.Print(err)
	}*/
	/*if err := nickTag.SetProperty("foreground-rgba", blueRGBA.Native()); err != nil {
		log.Print(err)
	}*/

	entry.Connect("activate", sendEntry)

	// taken from
	// https://github.com/jimmykarily/fuzzygui/blob/7ddb72ad712e7afa5bfcb2d06b435b74caeb8140/main.go#L88
	win.Connect("key-press-event", func(win *gtk.Window, event *gdk.Event) bool {
		ircMode := func(mode string) {
			pos := entry.GetPosition()
			pos = entry.InsertText(mode, pos)
			entry.SetPosition(pos)
		}

		keyEvent := gdk.EventKeyNewFromEvent(event)
		keyval := keyEvent.KeyVal()
		state := keyEvent.State()
		if (state & 0x4) != 0 {
			switch keyval {
			case gdk.KeyvalFromName("b"):
				ircMode("\x02")
				return true
			case gdk.KeyvalFromName("i"):
				ircMode("\x1d")
				return true
			case gdk.KeyvalFromName("u"):
				ircMode("\x1f")
				return true
			case gdk.KeyvalFromName("m"):
				ircMode("\x11")
				return true
			case gdk.KeyvalFromName("k"):
				ircMode("\x03")
				return true
			case gdk.KeyvalFromName("o"):
				ircMode("\x0f")
				return true
			}
		}

		return false
	})

	box.PackStart(chatScroll, true, true, 1)
	box.PackStart(entry, false, false, 1)

	win.ShowAll()
	entry.GrabFocus()
}

func main() {
	socketFd := flag.Int("fd", -1, "file descriptor of established socket")
	modeFlag := flag.String("mode", "msgpack", "protocol mode ('text' or 'msgpack')")
	myNick := flag.String("user", "", "Your username")

	flag.Parse()

	yconfig, err := ioutil.ReadFile(os.ExpandEnv("$HOME/test.yaml"))
	if err != nil {
		log.Print(err)
	} else {
		err = yaml.Unmarshal(yconfig, &config)
		if err != nil {
			log.Print(err)
		}
	}

	fmt.Println(config)

	if *myNick != "" {
		config.Nickname = *myNick
	}

	peerNick = defaultPeerNick

	switch *modeFlag {
	case "text":
		sock.SetMode(proto.ModeText)
	case "msgpack":
		sock.SetMode(proto.ModeMsgpack)
	default:
		fmt.Println("Invalid protocol mode:", *modeFlag)
		os.Exit(1)
	}

	if *socketFd >= 0 {
		err = sock.UseFD(*socketFd)
		if err != nil {
			log.Panic(err)
		}
	} else if *unixAddress != "" {
		fmt.Println("Connecting to:", *unixAddress)
		err = sock.Dial("unix", *unixAddress)
		if err != nil {
			log.Fatal(err)
		}

		err := sock.SendHeader()
		if err != nil {
			log.Panic(err)
		}
	} else {
		dest := flag.Arg(0)
		fmt.Println(dest)
		err = sock.Dial("tcp", dest)
		if err != nil {
			log.Fatal(err)
		}

		err := sock.SendHeader()
		if err != nil {
			log.Panic(err)
		}
	}

	chatWindow()

	sock.SetCallbacks(cb_Message, cb_Command, cb_Text, cb_Disconnect, cb_Status, cb_Roster, cb_Auth)
	sock.CB_RoomMemberJoin = cb_RoomMemberJoin
	sock.CB_RoomMemberPart = cb_RoomMemberPart

	gtk.Main()
}
