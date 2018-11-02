package main

import (
	"flag"
	"fmt"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/vmihailenco/msgpack"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

// Protocol modes
const (
	modeMsgpack = iota
	modeText    = iota
)

// Datum Types
const (
	dtAuth         = 0
	dtAuthResponse = 1
	dtCommand      = 2
	dtMessage      = 3
	dtRoster       = 4
	dtUser         = 5
)

// Datum structures
type dCommand struct {
	Cmd     string `msgpack:"cmd"`
	Payload string `msgpack:"payload"`
}

type dMessage struct {
	To    string   `msgpack:"to"`
	From  string   `msgpack:"from"`
	Flags []string `msgpack:"flags"`
	Date  int64    `msgpack:"date"`
	Msg   string   `msgpack:"msg"`
}

const defaultPeerNick = "them" // Used if we do not have chat partner's nick
const maxPacketSize = 9000

// User config variables
var config struct {
	Nickname string
	Address  string
}

// globals
var (
	sock          net.Conn
	sentFirstLine bool
	peerNick      string
	peerName    = flag.String("to", "", "Name of chat partner")
	modeSend    = modeMsgpack
	modeRecv    = modeMsgpack

	chat       *gtk.TextView
	chatBuffer *gtk.TextBuffer
	chatScroll *gtk.ScrolledWindow
	entry      *gtk.Entry
)

func timestamp(t time.Time) string {
	return t.Format("[15:04:05]")
}

func readSocket() {
	buffer := make([]byte, maxPacketSize)

	for {
		count, err := sock.Read(buffer)
		if err != nil {
			if err == io.EOF {
				msg := "Disconnected"
				fmt.Println(msg)
				glib.IdleAdd(func() bool {
					appendText(msg)
					return false
				})

				sock.Close()
				return
			}

			switch t := err.(type) {
			case *net.OpError:
				appendText(t.Error())
				return
			}

			log.Panicf("%v\n%t\n\n", err, err)
		}

		packet := buffer[:count]

		if modeRecv == modeMsgpack {
			if len(packet) < 3 {
				glib.IdleAdd(func() bool {
					appendText(fmt.Sprintf("Msgpack datum too small: 0x%x", len(packet)))
					return false
				})
				continue
			}

			dt := packet[0]
			size := (int(packet[1]) << 8) | int(packet[2])

			if len(packet) < size+3 {
				glib.IdleAdd(func() bool {
					appendText(fmt.Sprintf("Msgpack size header: 0x%x Actual size: 0x%x", size, len(packet)-3))
					return false
				})
				continue
			}
			datum := packet[3 : size+3]

			switch dt {
			case dtCommand:
				var cmd dCommand
				err = msgpack.Unmarshal(datum, &cmd)
				if err != nil {
					log.Panic(err)
				}

				processCommand(&cmd)
				if sock == nil {
					return
				}

			case dtMessage:
				var msg dMessage
				err = msgpack.Unmarshal(datum, &msg)
				if err != nil {
					log.Panic(err)
				}

				if msg.From != "" {
					peerNick = msg.From
				}

				glib.IdleAdd(func() bool {
					appendMsg(time.Now(), peerNick, msg.Msg)
					return false
				})
			default:
				glib.IdleAdd(func() bool {
					appendText(fmt.Sprintf("Unrecognized datum type: %v", dt))
					return false
				})
			}

		} else {
			if packet[0] == 0 {
				// Command
				if len(packet) >= 5 {
					cmd := dCommand{
						Cmd: string(packet[1:5]),
						Payload: string(packet[5:]),
					}

					processCommand(&cmd)
				}
			} else {
				// Message
				msg := packet[:count-1]

				fmt.Printf("%d %s\n", count, msg)

				glib.IdleAdd(func() bool {
					appendMsg(time.Now(), peerNick, string(msg))
					return false
				})
			}
		}
	}
	sock.Close()
}

func processCommand(cmd *dCommand) {
	switch cmd.Cmd {
	case "BYE ":
		sock.Close()
		sock = nil

		glib.IdleAdd(func() bool {
			appendText("Disconnected: BYE")
			return false
		})
	case "NICK":
		if cmd.Payload != "" {
			oldNick := peerNick
			peerNick = cmd.Payload
			glib.IdleAdd(func() bool {
				appendText(fmt.Sprintf("%s is now known as %s", oldNick, cmd.Payload))
				return false
			})
		} else {
			peerNick = defaultPeerNick
		}
	case "TEXT":
		modeRecv = modeText
	case "MPCK":
		modeRecv = modeMsgpack
	default:
		glib.IdleAdd(func() bool {
			appendMsg(time.Now(), peerNick, cmd.Cmd + " " + cmd.Payload)
			return false
		})
	}

}

func sendDatum(msg interface{}) {
	datum, err := msgpack.Marshal(msg)
	if err != nil {
		log.Panic(err)
	}

	var dt int

	switch msg.(type) {
	case *dCommand:
		dt = dtCommand
	case *dMessage:
		dt = dtMessage
	default:
		log.Panicf("Tried to send unknown datum type: %v %t", msg, msg)
	}

	packet := make([]byte, 0, len(datum)+3)
	packet = append(packet, byte(dt), byte(len(datum)>>8), byte(len(datum)&0xFF))
	packet = append(packet, datum...)
	fmt.Println(packet[0], packet[1:3], len(datum), string(packet))
	_, err = sock.Write(packet)
	if err != nil {
		log.Panic(err)
	}
}

func sendCommand(cmd *dCommand) {
	switch modeSend {
	case modeText:
		c := make([]byte, 0, 1 + len(cmd.Cmd) + len(cmd.Payload))
		c = append(c, 0)
		c = append(c, []byte(cmd.Cmd)...)
		c = append(c, []byte(cmd.Payload)...)
		sock.Write(c)
	default:
		sendDatum(cmd)
	}
}

func sendMessage(msg *dMessage) {
	if modeSend == modeText {
		_, err := sock.Write([]byte(msg.Msg + "\r"))
		if err != nil {
			log.Panic(err)
		}
	} else {
		sendDatum(msg)
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

	if entryText == "" || sock == nil {
		return
	}

	if entryText[0] == '/' {
		appendText(entryText)

		entry.SetText("")
		c := strings.SplitN(entryText[1:], " ", 2)

		cmd := dCommand{
			Cmd: c[0],
		}

		if len(c) > 1 {
			cmd.Payload = c[1]
		}

		switch cmd.Cmd {
		case "nick":
			cmd.Cmd = "NICK"
			sendCommand(&cmd)
		case "bye":
			cmd.Cmd = "BYE "
			sendCommand(&cmd)
		case "msgpack":
			cmd.Cmd = "MPCK"
			sendCommand(&cmd)
			modeSend = modeMsgpack
		case "text":
			cmd.Cmd = "TEXT"
			sendCommand(&cmd)
			modeSend = modeText
		}
	} else {
		appendMsg(time.Now(), config.Nickname, entryText)

		entry.SetText("")

		msg := dMessage{
			To:    *peerName,
			From:  config.Nickname,
			Flags: []string{},
			Date:  time.Now().Unix(),
			Msg:   entryText,
		}

		sendMessage(&msg)
	}
}

func chatWindow() {
	gtk.Init(nil)

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Panic(err)
	}

	win.SetTitle("Flexim")
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

	flag.Parse()

	yconfig, err := ioutil.ReadFile("/home/matt/test.yaml")
	if err != nil {
		log.Print(err)
	} else {
		err = yaml.Unmarshal(yconfig, &config)
		if err != nil {
			log.Print(err)
		}
	}

	fmt.Println(config)

	peerNick = defaultPeerNick

	switch *modeFlag {
	case "text":
		modeSend = modeText
		modeRecv = modeText
	case "msgpack":
		modeSend = modeMsgpack
		modeRecv = modeMsgpack
	default:
		fmt.Println("Invalid protocol mode:", *modeFlag)
		os.Exit(1)
	}

	if *socketFd >= 0 {
		fd := os.NewFile(uintptr(*socketFd), "")
		if fd == nil {
			log.Fatal("Invalid file descriptor")
		}

		sock, err = net.FileConn(fd)
		if err != nil {
			log.Panic(err)
		}
	} else {
		dest := flag.Arg(0)
		fmt.Println(dest)
		sock, err = net.Dial("tcp", dest)
		if err != nil {
			log.Fatal(err)
		}

		if modeSend == modeMsgpack {
			sock.Write([]byte("\xa4FLEX"))
		} else {
			sock.Write([]byte("\x00FLEX"))
		}
	}

	chatWindow()

	go readSocket()

	gtk.Main()
}
