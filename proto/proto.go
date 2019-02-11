package proto

import (
	"bytes"
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
	ModeText
)

// Protocol headers
var (
	HeaderMsgpack = []byte{'\xa4', 'F', 'L', 'E', 'X'}
	HeaderText    = []byte{'\x00', 'F', 'L', 'E', 'X'}
)

// Datum Types
const (
	DAuth         = 0
	DAuthResponse = 1
	DCommand      = 2
	DMessage      = 3
	DRoster       = 4
	DUser         = 5
	DStatus       = 6
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

type Status struct {
	Status  int8   `msgpack:"status"`
	Payload string `msgpack:"payload"`
}

type Roster []User

type User struct {
	Aliases  []string `msgpack:"aliases"`
	Key      []byte   `msgpack:"key"`
	LastSeen int64    `msgpack:"last_seen"`
}

type Socket struct {
	conn          net.Conn
	gotHeader     bool
	modeSend      int
	modeRecv      int
	cb_Message    func(*Message)
	cb_Command    func(*Command)
	cb_Text       func(string)
	cb_Disconnect func()
	cb_Status     func(*Status)
}

func printMsgpack(data []byte) {
	var unpacked interface{}

	err := msgpack.Unmarshal(data, &unpacked)
	if err != nil {
		log.Panic(err)
	}

	fmt.Printf("msgpack: %+v\n", unpacked)
}

func newSocket(conn net.Conn, mode int) Socket {
	return Socket{conn, false, mode, mode, nil, nil, nil, nil, nil}
}

func Dial(protocol string, addr string, mode int) (*Socket, error) {

	conn, err := net.Dial(protocol, addr)
	if err != nil {
		return nil, err
	}

	s := newSocket(conn, mode)

	fmt.Printf("Dial in mode: %d, %d\n", s.modeRecv, s.modeSend)
	return &s, nil
}

func FromConn(sock net.Conn, mode int) *Socket {
	s := newSocket(sock, mode)
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

	s := newSocket(sock, mode)
	fmt.Printf("FromFD in mode: %d, %d\n", s.modeRecv, s.modeSend)

	return &s, nil
}

func (s *Socket) Read(buffer []byte) (int, error) {
	return s.conn.Read(buffer)
}

func (s *Socket) Write(buffer []byte) (int, error) {
	return s.conn.Write(buffer)
}

func (s *Socket) SetCallbacks(message func(*Message), command func(*Command), txt func(string), disconnect func(), status func(*Status)) {
	s.cb_Message = message
	s.cb_Command = command
	s.cb_Text = txt
	s.cb_Disconnect = disconnect
	s.cb_Status = status
	go s.readSocket()
}

func (s *Socket) Close() error {
	if s == nil {
		return errors.New("Cannot close nil Socket")
	}

	s.cb_Disconnect()
	return s.conn.Close()
}

func (s *Socket) sendDatum(msg interface{}) error {
	if s == nil {
		return errors.New("Cannot send datum to nil Socket")
	}

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
	case *Status:
		dt = DStatus
	default:
		log.Panicf("Tried to send unknown datum type: %v %t", msg, msg)
	}

	packet := make([]byte, 0, len(datum)+3)
	packet = append(packet, byte(dt), byte(len(datum)>>8), byte(len(datum)&0xFF))
	packet = append(packet, datum...)
	//fmt.Println(packet[0], packet[1:3], len(datum), string(packet))

	_, err = s.conn.Write(packet)
	if err != nil {
		fmt.Println("Error sending packet")
	}

	return err
}

func (s *Socket) SendHeader() error {
	if s == nil {
		return errors.New("Cannot send header to nil Socket")
	}

	s.gotHeader = true

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

func (s *Socket) ReceiveHeader() error {
	header := make([]byte, 5)

	_, err := io.ReadFull(s, header)
	if err != nil {
		return err
	}

	if bytes.Equal(header, HeaderMsgpack) {
		s.modeRecv = ModeMsgpack
		s.modeSend = ModeMsgpack
		s.gotHeader = true

	} else if bytes.Equal(header, HeaderText) {
		s.modeRecv = ModeText
		s.modeSend = ModeText
		s.gotHeader = true

	} else {
		return fmt.Errorf("Invalid Header: %+v", header)
	}

	return nil
}

func (s *Socket) SendCommand(cmd *Command) error {
	if s == nil {
		return errors.New("Cannot send command to nil Socket")
	}

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
	if s == nil {
		return errors.New("Cannot send message to nil Socket")
	}

	if s.modeSend == ModeText {
		_, err := s.Write([]byte(msg.Msg + "\r"))
		return err
	} else {
		return s.sendDatum(msg)
	}

}

func (s *Socket) SendStatus(status *Status) error {
	if s == nil {
		return errors.New("Cannot send message to nil Socket")
	}

	if s.modeSend == ModeText {
		_, err := s.Write([]byte(fmt.Sprintf("%d %s\r", status.Status, status.Payload)))
		return err
	} else {
		return s.sendDatum(status)
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

	if !s.gotHeader {
		if err := s.ReceiveHeader(); err != nil {
			s.Close()

			s.cb_Text(fmt.Sprintf("Error reading header: %s", err))
			return
		}
	}

	for {
		var err error
		if s.modeRecv == ModeMsgpack {
			err = s.readMsgpack()
		} else {
			err = s.readText()
		}

		if err != nil {
			if err == io.EOF {
				s.cb_Text("Disconnected")
				break
			}

			switch t := err.(type) {
			case *net.OpError:
				s.cb_Text(t.Error())
				break
			}

			s.cb_Text(err.Error())
		}
	}

	s.Close()
}

func (s *Socket) readMsgpack() error {
	header := make([]byte, 3)

	_, err := io.ReadFull(s, header)
	if err != nil {
		return err
	}

	// first byte is datum type
	dt := header[0]
	// 2nd and 3rd bytes are datum length in bytes
	size := (int(header[1]) << 8) | int(header[2])

	datum := make([]byte, size)
	_, err = io.ReadFull(s, datum)
	if err != nil {
		s.cb_Text(err.Error())
		return err
	}

	switch dt {
	case DCommand:
		var cmd Command
		err = msgpack.Unmarshal(datum, &cmd)
		if err != nil {
			log.Panic(err)
		}

		s.processCommand(&cmd)
		if s == nil {
			return nil
		}

	case DMessage:
		var msg Message
		err = msgpack.Unmarshal(datum, &msg)
		if err != nil {
			return err
		}

		s.cb_Message(&msg)

	case DStatus:
		var status Status
		err = msgpack.Unmarshal(datum, &status)
		if err != nil {
			return err
		}

		if status.Status == 0 && status.Payload == "" {
			fmt.Println("Empty status received.")
			printMsgpack(datum)
		}

		s.cb_Status(&status)

	case DRoster:
		var roster Roster
		err = msgpack.Unmarshal(datum, &roster)
		if err != nil {
			return err
		}

		fmt.Printf("Roster: %+v\n", roster)

	// Not currently handled
	case DAuth:
		s.cb_Text("Auth datum not handled")
	case DAuthResponse:
		s.cb_Text("AuthResponse datum not handled")
	case DUser:
		s.cb_Text("User datum not handled")

	default:
		s.cb_Text(fmt.Sprintf("Unrecognized datum type: %v", dt))
	}

	return nil
}

func (s *Socket) readText() error {
	buffer := make([]byte, 1500)
	count, err := s.Read(buffer)
	if err != nil {
		s.cb_Text(err.Error())
		return err
	}

	if buffer[0] == 0 {
		// Command
		if len(buffer) >= 5 {
			cmd := Command{
				Cmd:     string(buffer[1:5]),
				Payload: []string{string(buffer[5:])},
			}

			s.processCommand(&cmd)
		}
	} else {
		// Message
		msg := buffer[:count-1]

		fmt.Printf("%d %s\n", count, msg)
		msgObj := Message{
			Msg: string(msg),
		}
		s.cb_Message(&msgObj)
	}

	return nil
}
