package main

// Notes:
// - the clientMap needs a mutex
// - the server socket probably needs a mutex
// - maybe the client sockets need mutexes?

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/mnakama/flexim-go/proto"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Channel struct {
	members []string
}

// User config variables
var config struct {
	UseTLS         bool
	TLSNoVerify    bool
	Address        string
	Username       string
	Nickname       string
	Realname       string
	Password       string
	ServerPassword string
}

var (
	irc        net.Conn
	lastClient *proto.Socket
	channels   = make(map[string]Channel)
	clientMap  = make(map[string]*proto.Socket, 1)
	myHostname string
	tcplisten  = flag.String("tcplisten", "", "bind address for TCP clients")
	unixlisten = flag.String("listen", "", "bind address for local clients")

	// X.org crashes at about 50+ visible windows with dwm
	chatLimit = flag.Int("chatlimit", 30, "flood protection: maximum amount of open chats")
)

func login() (err error) {
	if !config.UseTLS {
		irc, err = net.Dial("tcp", config.Address)
	} else {
		tlsConfig := tls.Config{}
		if config.TLSNoVerify {
			tlsConfig.InsecureSkipVerify = true
		}
		irc, err = tls.Dial("tcp", config.Address, &tlsConfig)
	}
	if err != nil {
		return
	}

	if config.ServerPassword != "" {
		// don't echo the password
		fmt.Println("PASS :********")
		fmt.Fprintf(irc, "PASS :%s\n", config.ServerPassword)
	}
	sendIRCCmd(fmt.Sprintf("NICK %s", config.Nickname))
	sendIRCCmd(fmt.Sprintf("USER %s 0 * :%s", config.Username, config.Realname))

	go listenServer(irc)

	return
}

func guessMask() string {
	return fmt.Sprintf("%s!~%s@%s", config.Nickname, config.Username, myHostname)
}

func getMaskLen() (maskLen int) {
	maskLen = len(config.Nickname) + len(config.Username) + 3 // len("!~@")
	if myHostname == "" {
		maskLen += 50
	} else {
		maskLen += len(myHostname)
	}

	return
}

func setHostname(hostname string) {
	myHostname = hostname
	log.Printf("set hostname = '%s'", myHostname)
	log.Printf("guessed mask = '%s'", guessMask())
}

func leaveChannel(channel string) {
	delete(channels, channel)

	sendIRCCmd(fmt.Sprintf("PART %s", channel))
}

func getClientID(from, to string) (id string) {
	if strings.HasPrefix(to, "#") {
		id = to
	} else {
		nickIDX := strings.Index(from, "!")
		if nickIDX > 0 {
			id = from[:nickIDX]
		} else {
			id = from
		}
	}

	id = strings.ToLower(id)

	return
}

func sendIRCCmd(cmd string) {
	if irc == nil {
		log.Print("cannot send command; irc is nil")
		return
	}
	fmt.Printf("%s\n", cmd)
	fmt.Fprintf(irc, "%s\n", cmd)
}

func processIRCLine(line string) {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	fmt.Println(line)

	if strings.HasPrefix(line, "PING ") {
		cmd := fmt.Sprintf("PONG %s", line[5:])
		sendIRCCmd(cmd)

		return
	}

	var (
		//tags   string
		source string
		verb   string
		params []string
	)

	{
		// code block for temporary vars that I don't want leaking into the rest of the code
		fields := strings.Fields(line)

		if strings.HasPrefix(fields[0], "@") {
			//tags = fields[0][1:]
			fields = fields[1:]
		}

		if strings.HasPrefix(fields[0], ":") {
			source = fields[0][1:]
			fields = fields[1:]
		}

		verb = fields[0]
		fields = fields[1:]

		for i, field := range fields {
			if strings.HasPrefix(field, ":") {
				newfields := fields[:i]
				longfield := strings.Join(fields[i:], " ")[1:]
				newfields = append(newfields, longfield)
				fields = newfields
				break
			}
		}

		params = fields
	}

	if verb == "PRIVMSG" || verb == "NOTICE" {
		to := params[0]
		text := params[1]

		if to == "*" {
			idx := strings.Index(text, "Found your hostname: ")
			if idx > 0 {
				setHostname(text[idx+21:])
			}
			// only needs to be in status window
			return
		}

		clientID := getClientID(source, to)
		msg := proto.Message{
			To:   to,
			From: source,
			Msg:  text,
		}
		sendToClient(clientID, msg)

	} else if verb == "JOIN" {
		channel := params[0]
		msg := proto.Message{
			To:   channel,
			From: channel,
			Msg:  fmt.Sprintf("%s has joined the channel", source),
		}

		sendToClient(channel, msg)
	} else if verb == "PART" {
		channel := params[0]
		var partMsg string
		if len(params) > 1 {
			partMsg = params[1]
		}

		msg := proto.Message{
			To:   channel,
			From: channel,
			Msg:  fmt.Sprintf("%s has left the channel (%s)", source, partMsg),
		}

		sendToClient(channel, msg)
	} else if verb == "QUIT" {
		quitNick := nickFromMask(source)
		quitMsg := params[0]

		for channelName, c := range channels {
			for _, member := range c.members {
				if quitNick == member {
					msg := proto.Message{
						To:   channelName,
						From: channelName,
						Msg:  fmt.Sprintf("%s has quit (%s)", source, quitMsg),
					}
					sendToClient(channelName, msg)
					break
				}
			}
		}

		client, found := clientMap[quitNick]
		if found {
			msg := proto.Message{
				To:   source,
				From: source,
				Msg:  fmt.Sprintf("%s has quit (%s)", source, quitMsg),
			}
			client.SendMessage(&msg)
		}

	} else if verb == "332" {
		to := params[0]
		channel := params[1]
		topic := params[2]

		// convert pipes to newlines
		topic = strings.ReplaceAll(topic, " | ", "\n  ")

		msg := proto.Message{
			To:   to,
			From: channel,
			Msg:  fmt.Sprintf("Topic: %s", topic),
		}
		sendToClient(channel, msg)
	} else if verb == "333" {
		to := params[0]
		channel := params[1]
		who := params[2]
		whenInt, _ := strconv.ParseInt(params[3], 10, 64)

		when := time.Unix(whenInt, 0)
		msg := proto.Message{
			To:   to,
			From: channel,
			Msg: fmt.Sprintf("Topic set by %s on %s",
				who, when.Format("2006/01/02 15:04 MST")),
		}
		sendToClient(channel, msg)

	} else if verb == "353" {
		// list of nicknames when joining a channel
		//to := fields[2]
		channel := params[2]
		members := strings.Split(params[3], " ")

		addChannelMembers(channel, members)
	} else if verb == "354" {
		// list of users and masks when running /who
	} else if verb == "315" {
		// end of /who list
	} else if verb == "366" {
		to := params[0]
		channelName := params[1]

		channel := channels[channelName]
		members := channel.members
		text := fmt.Sprintf("People in this channel: %s", strings.Join(members, ", "))
		msg := proto.Message{
			To:   to,
			From: channelName,
			Msg:  text,
		}

		sendToClient(channelName, msg)
	} else {
		if lastClient != nil {
			msg := proto.Message{
				To:   "*",
				From: source,
				Msg:  line,
			}
			lastClient.SendMessage(&msg)
		}
	}

}

func listenServer(irc net.Conn) {
	var reader *bufio.Reader
	reader = bufio.NewReader(irc)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			log.Print(err)
			if err == io.EOF {
				quit(0)
			}
			return
		}

		if len(line) < 1 {
			continue
		}

		processIRCLine(line)
	}
}

func addChannelMembers(channel string, members []string) {
	c, found := channels[channel]
	if !found {
		c = Channel{}
	}

	for _, member := range members {
		member = strings.TrimPrefix(member, "@")
		member = strings.TrimPrefix(member, "+")
		c.members = append(c.members, member)
	}

	channels[channel] = c
}

func nickFromMask(mask string) string {
	idx := strings.Index(mask, "!")
	if idx > -1 {
		return mask[:idx]
	} else {
		return mask
	}
}

func sendToClient(clientID string, msg proto.Message) {
	client := getOrStartClient(clientID)
	client.SendMessage(&msg)
}

func reconnect() {
	for {
		time.Sleep(time.Second)
		if err := login(); err != nil {
			log.Println(err)
		} else {
			return
		}
	}
}

func cb_Status(status *proto.Status) {
	log.Println(status)
}

func cb_Roster(roster *proto.Roster) {
	log.Println(roster)
}

func cb_Auth(auth *proto.Auth) {
	log.Println(auth)
}

func getOrStartClient(clientID string) (client *proto.Socket) {
	var found bool
	client, found = clientMap[clientID]
	if !found {
		client = newChatIn(clientID)
	}

	return
}

func newChatIn(clientID string) (client *proto.Socket) {
	if len(clientMap) >= *chatLimit {
		fmt.Printf("Too many open chats! (%d)\n", len(clientMap))
		return
	}

	fd, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Panic(err)
	}

	var sock proto.Socket
	client = &sock
	sock.SetMode(proto.ModeMsgpack)

	err = sock.UseFD(fd[0])
	if err != nil {
		log.Panic(err)
	}

	err = sock.SendHeader()
	if err != nil {
		log.Panic(err)
	}

	clientFile := os.NewFile(uintptr(fd[1]), "")
	if clientFile == nil {
		log.Panicln("Could not get file from file descriptor:", fd[1])
	}
	defer clientFile.Close()

	// exec
	pattr := os.ProcAttr{
		Files: []*os.File{nil, os.Stdout, os.Stderr, clientFile},
	}

	proc, err := os.StartProcess("flexim-chat", []string{"flexim-chat", "--fd", "3", "--mode", "msgpack", "--to", clientID, "--user", config.Nickname}, &pattr)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("Pid: %v", proc.Pid)

	clientMap[clientID] = &sock

	setCallbacks(&sock, clientID)

	return
}

func newChatOut(conn net.Conn) {
	sock := proto.FromConn(conn, proto.ModeMsgpack)

	setCallbacks(sock, "")
}

func setCallbacks(sock *proto.Socket, clientID string) {
	const maxIRCLen = 510

	sock.SetCallbacks(func(msg *proto.Message) { //msg
		log.Printf("client -> server: %+v\n", msg)
		if irc == nil {
			log.Println("irc is nil")
			return
		}
		if clientID == "" && msg.To != "" {
			clientID = strings.ToLower(msg.To)
			clientMap[clientID] = sock

			if strings.HasPrefix(clientID, "#") {
				cmd := fmt.Sprintf("JOIN %s", clientID)
				sendIRCCmd(cmd)
			}
		}

		// the maximum command length needs to account for what the IRC server will send
		// to other clients. Full host mask, plus : and a space before PRIVMSG starts
		cmdLen := maxIRCLen - getMaskLen() - 2
		ircCmd := fmt.Sprintf("PRIVMSG %s :%s", msg.To, msg.Msg)
		for len(ircCmd) > cmdLen {
			sendIRCCmd(ircCmd[:cmdLen])
			ircCmd = fmt.Sprintf("PRIVMSG %s :%s", msg.To, ircCmd[cmdLen:])
		}
		sendIRCCmd(ircCmd)

		lastClient = sock
	}, func(cmd *proto.Command) { // cmd
		log.Println(cmd)
		switch cmd.Cmd {
		case "JOIN":
			channel := clientID
			if len(cmd.Payload) > 0 {
				channel = cmd.Payload[0]
			}
			sendIRCCmd(fmt.Sprintf("JOIN %s", channel))
		case "PART":
			channel := clientID
			if len(cmd.Payload) > 0 {
				channel = cmd.Payload[0]
			}

			leaveChannel(channel)
		case "QUIT":
			sendIRCCmd("QUIT")
			quit(0)
		}

		lastClient = sock
	}, func(txt string) { // text
		log.Println(txt)

	}, func() { // disconnect
		log.Println("Chat window disconnected")
		if clientID != "" {
			/*if strings.HasPrefix(clientID, "#") {
				fmt.Fprintf(irc, "PART %s\n", clientID)
			}*/
			delete(clientMap, clientID)
		}

	},
		cb_Status, // status
		cb_Roster, // roster
		cb_Auth)   // auth
}

func listenLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		newChatOut(conn)
	}
}

// Catch interrupt signal
func waitSignal() {
	c := make(chan os.Signal)

	signal.Notify(c, os.Interrupt)
	s := <-c
	log.Println("Received signal:", s)

	quit(0)
}

// do cleanup
func quit(ret int) {
	fmt.Println("Closing down...")
	os.Remove(*unixlisten)

	cmd := proto.Command{
		Cmd: "BYE ",
	}
	for _, sock := range clientMap {
		sock.SendCommand(&cmd)
		sock.Close()
	}

	os.Exit(ret)
}

func termInput(server net.Conn) {
	input := bufio.NewReader(os.Stdin)
	for {
		msg, err := input.ReadString('\n')
		if err != nil {
			fmt.Println(err)
		}

		fmt.Fprintln(server, msg)
	}
}

func main() {
	flag.Parse()

	// read config
	yconfig, err := ioutil.ReadFile(os.ExpandEnv("$HOME/.config/flexim/irc.yaml"))
	if err != nil {
		log.Print(err)
	} else {
		err = yaml.Unmarshal(yconfig, &config)
		if err != nil {
			log.Print(err)
		}
	}

	fmt.Println(config)

	// connect
	if err := login(); err != nil {
		log.Fatal(err)
	}

	// listen to stdin
	go termInput(irc)

	// maybe listen to a tcp socket for clients
	if *tcplisten != "" {
		ln, err := net.Listen("tcp", *tcplisten)
		if err != nil {
			log.Fatal(err)
		}
		go listenLoop(ln)
	}

	// listen to a unix socket for clients
	if *unixlisten == "" {
		*unixlisten = "/tmp/" + config.Address
	}

	os.Remove(*unixlisten)
	ln, err := net.Listen("unix", *unixlisten)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		r := recover()
		if r != nil {
			fmt.Println("Panic:", r)
		}

		quit(1)
	}()

	go listenLoop(ln)

	waitSignal()
}
