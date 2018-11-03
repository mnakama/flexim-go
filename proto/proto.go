package proto

import (
	"errors"
	"fmt"
	"github.com/vmihailenco/msgpack"
	"io"
	"log"
	"net"
	"os"
)

const maxPacketSize = 9000

// Protocol modes
const (
	ModeMsgpack = iota
	ModeText    = iota
)

// Datum Types
const (
	DAuth         = 0
	DAuthResponse = 1
	DCommand      = 2
	DMessage      = 3
	DRoster       = 4
	DUser         = 5
)

// Datum structures
type Command struct {
	Cmd     string   `msgpack:"cmd"`
	Payload []string `msgpack:"payload"`
}

type Message struct {
	To    string   `msgpack:"to"`
	From  string   `msgpack:"from"`
	Flags []string `msgpack:"flags"`
	Date  int64    `msgpack:"date"`
	Msg   string   `msgpack:"msg"`
}

type Socket struct {
	conn          net.Conn
	modeSend      int
	modeRecv      int
	cb_Message    func(*Message)
	cb_Command    func(*Command)
	cb_Text       func(string)
	cb_Disconnect func()
}

func Dial(protocol string, addr string, mode int) (*Socket, error) {

	conn, err := net.Dial(protocol, addr)
	if err != nil {
		return nil, err
	}

	s := Socket{conn, mode, mode, nil, nil, nil, nil}

	fmt.Printf("Dial in mode: %d, %d\n", s.modeRecv, s.modeSend)
	return &s, nil
}

func FromConn(sock net.Conn, mode int) *Socket {
	s := Socket{sock, mode, mode, nil, nil, nil, nil}
	return &s
}

func FromFD(fd int, mode int) (*Socket, error) {
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		return nil, errors.New("Invalid file descriptor")
	}

	sock, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}

	s := Socket{sock, mode, mode, nil, nil, nil, nil}
	fmt.Printf("FromFD in mode: %d, %d\n", s.modeRecv, s.modeSend)

	return &s, nil
}

func (s *Socket) Read(buffer []byte) (int, error) {
	return s.conn.Read(buffer)
}

func (s *Socket) Write(buffer []byte) (int, error) {
	return s.conn.Write(buffer)
}

func (s *Socket) SetCallbacks(message func(*Message), command func(*Command), txt func(string), disconnect func()) {
	s.cb_Message = message
	s.cb_Command = command
	s.cb_Text = txt
	s.cb_Disconnect = disconnect
	go s.readSocket()
}

func (s *Socket) Close() error {
	s.cb_Disconnect()
	return s.conn.Close()
}

func (s *Socket) sendDatum(msg interface{}) error {
	datum, err := msgpack.Marshal(msg)
	if err != nil {
		return err
	}

	var dt int

	switch msg.(type) {
	case *Command:
		dt = DCommand
	case *Message:
		dt = DMessage
	default:
		log.Panicf("Tried to send unknown datum type: %v %t", msg, msg)
	}

	packet := make([]byte, 0, len(datum)+3)
	packet = append(packet, byte(dt), byte(len(datum)>>8), byte(len(datum)&0xFF))
	packet = append(packet, datum...)
	fmt.Println(packet[0], packet[1:3], len(datum), string(packet))
	_, err = s.conn.Write(packet)

	return err
}

func (s *Socket) SendHeader() error {
	switch s.modeSend {
	case ModeText:
		_, err := s.Write([]byte("\x00FLEX"))
		return err
	case ModeMsgpack:
		_, err := s.Write([]byte("\xa4FLEX"))
		return err
	}

	return errors.New("Invalid send mode is set")
}

func (s *Socket) SendCommand(cmd *Command) error {
	switch s.modeSend {
	case ModeText:
		var payloadStr string
		if len(cmd.Payload) > 0 {
			payloadStr = cmd.Payload[0]
		}

		c := make([]byte, 0, 1+len(cmd.Cmd)+len(payloadStr))
		c = append(c, 0)
		c = append(c, []byte(cmd.Cmd)...)
		c = append(c, []byte(payloadStr)...)
		_, err := s.Write(c)
		return err
	default:
		return s.sendDatum(cmd)
	}
}

func (s *Socket) SendMessage(msg *Message) error {
	if s.modeSend == ModeText {
		_, err := s.Write([]byte(msg.Msg + "\r"))
		return err
	} else {
		return s.sendDatum(msg)
	}

}

func (s *Socket) SetSendMode(mode int) {
	var ctext string

	switch mode {
	case ModeText:
		ctext = "TEXT"
	case ModeMsgpack:
		ctext = "MPCK"
	default:
		log.Panicln("Invalid send mode:", mode)
	}

	cmd := Command{
		Cmd:     ctext,
		Payload: []string{},
	}
	s.SendCommand(&cmd)

	s.modeSend = mode
}

func (s *Socket) processCommand(cmd *Command) {
	switch cmd.Cmd {
	case "BYE ":
		s.Close()

		s.cb_Text("Disconnected: BYE")
	case "TEXT":
		s.modeRecv = ModeText
	case "MPCK":
		s.modeRecv = ModeMsgpack
	default:
		s.cb_Command(cmd)
	}
}

func (s *Socket) readSocket() {
	buffer := make([]byte, maxPacketSize)

	for {
		count, err := s.Read(buffer)
		if err != nil {
			if err == io.EOF {
				msg := "Disconnected"
				s.Close()
				s.cb_Text(msg)

				return
			}

			switch t := err.(type) {
			case *net.OpError:
				s.cb_Text(t.Error())
				return
			}

			log.Panicf("%v\n%t\n\n", err, err)
		}

		packet := buffer[:count]

		if s.modeRecv == ModeMsgpack {
			if len(packet) < 3 {
				s.cb_Text(fmt.Sprintf("Msgpack datum too small: 0x%x", len(packet)))
				continue
			}

			dt := packet[0]
			size := (int(packet[1]) << 8) | int(packet[2])

			if len(packet) < size+3 {
				s.cb_Text(fmt.Sprintf("Msgpack size header: 0x%x Actual size: 0x%x", size, len(packet)-3))
				continue
			}
			datum := packet[3 : size+3]

			switch dt {
			case DCommand:
				var cmd Command
				err = msgpack.Unmarshal(datum, &cmd)
				if err != nil {
					log.Panic(err)
				}

				s.processCommand(&cmd)
				if s == nil {
					return
				}

			case DMessage:
				var msg Message
				err = msgpack.Unmarshal(datum, &msg)
				if err != nil {
					log.Panic(err)
				}

				s.cb_Message(&msg)
			default:
				s.cb_Text(fmt.Sprintf("Unrecognized datum type: %v", dt))
			}

		} else {
			if packet[0] == 0 {
				// Command
				if len(packet) >= 5 {
					cmd := Command{
						Cmd:     string(packet[1:5]),
						Payload: []string{string(packet[5:])},
					}

					s.processCommand(&cmd)
				}
			} else {
				// Message
				msg := packet[:count-1]

				fmt.Printf("%d %s\n", count, msg)
				msgObj := Message{
					Msg: string(msg),
				}
				s.cb_Message(&msgObj)
			}
		}
	}
	s.Close()
}
