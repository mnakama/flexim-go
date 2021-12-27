package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

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
	return t.Format("[15:04:05]")
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

	//scrollToBottom()
}

func appendMsg(t time.Time, who string, msg string) {
	appendText(fmt.Sprintf("%s %s: %s", timestamp(t), who, msg))
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
		case "join":
			cmd.Cmd = "JOIN"
			sock.SendCommand(&cmd)
		case "part":
			cmd.Cmd = "PART"
			sock.SendCommand(&cmd)
		case "quit":
			cmd.Cmd = "QUIT"
			sock.SendCommand(&cmd)
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

	entry.Connect("activate", sendEntry)

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

	gtk.Main()
}
