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
	ircStyle "github.com/mnakama/flexim-go/pkg/irc-style"
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

func appendMsg(t time.Time, who string, msg string) {
	end := chatBuffer.GetEndIter()

	timestampText := "<tt>" + timestamp(t) + " </tt>"

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
	chatBuffer.InsertMarkup(end, "<b>"+escapeAngles(who)+"</b><tt> </tt>")

	msg = escapeAngles(msg)
	msg = ircStyle.IRCToPango(msg)

	end = chatBuffer.GetEndIter()
	chatBuffer.InsertMarkup(end, msg)
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
			case gdk.KeyvalFromName("s"):
				ircMode("\x1e")
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
