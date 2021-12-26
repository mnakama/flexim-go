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

func login() (irc net.Conn, err error) {
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
		fmt.Fprintf(irc, "PASS :%s\n", config.ServerPassword)
	}
	fmt.Fprintf(irc, "NICK %s\n", config.Nickname)
	fmt.Fprintf(irc, "USER %s 0 * :%s\n", config.Username, config.Realname)

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

		line = line[:len(line)-1]
		fmt.Println(line)

		if strings.HasPrefix(line, "PING ") {
			msg := fmt.Sprintf("PONG %s\n", line[5:])
			fmt.Print(msg)
			fmt.Fprint(irc, msg)

			continue
		}

		if strings.HasPrefix(line, ":") {
			fields := strings.Fields(line)
			from := fields[0][1:]
			msgType := fields[1]

			if msgType == "PRIVMSG" || msgType == "NOTICE" {
				to := fields[2]
				text := strings.Join(fields[3:], " ")
				text = strings.TrimPrefix(text, ":")

				if to == "*" {
					idx := strings.Index(text, "Found your hostname: ")
					if idx > 0 {
						setHostname(text[idx+21:])
					}
					// only needs to be in status window
					continue
				}

				msg := proto.Message{
					To:   to,
					From: from,
					Msg:  text,
				}

				clientID := getClientID(from, to)
				sendToClient(clientID, msg)
			} else if msgType == "JOIN" {
				channel := fields[2]
				msg := proto.Message{
					To:   channel,
					From: channel,
					Msg:  fmt.Sprintf("%s has joined the channel", from),
				}

				sendToClient(channel, msg)
			} else if msgType == "PART" {
				channel := fields[2]
				var partMsg string
				if len(fields) > 2 {
					partMsg = strings.TrimPrefix(strings.Join(fields[3:], " "), ":")
				}

				msg := proto.Message{
					To:   channel,
					From: channel,
					Msg:  fmt.Sprintf("%s has left the channel (%s)", from, partMsg),
				}

				sendToClient(channel, msg)
			} else if msgType == "QUIT" {
				quitNick := nickFromMask(from)
				quitMsg := strings.TrimPrefix(strings.Join(fields[2:], " "), ":")

				for channelName, c := range channels {
					for _, member := range c.members {
						if quitNick == member {
							msg := proto.Message{
								To:   channelName,
								From: channelName,
								Msg:  fmt.Sprintf("%s has quit (%s)", from, quitMsg),
							}
							sendToClient(channelName, msg)
							break
						}
					}
				}

				client, found := clientMap[quitNick]
				if found {
					msg := proto.Message{
						To:   from,
						From: from,
						Msg:  fmt.Sprintf("%s has quit (%s)", from, quitMsg),
					}
					client.SendMessage(&msg)
				}

			} else if msgType == "332" {
				to := fields[2]
				channel := fields[3]
				topic := strings.TrimPrefix(strings.Join(fields[4:], " "), ":")

				// convert pipes to newlines
				topic = strings.ReplaceAll(topic, " | ", "\n  ")

				msg := proto.Message{
					To:   to,
					From: channel,
					Msg:  fmt.Sprintf("Topic: %s", topic),
				}
				sendToClient(channel, msg)
			} else if msgType == "333" {
				to := fields[2]
				channel := fields[3]
				who := fields[4]
				whenInt, _ := strconv.ParseInt(fields[5], 10, 64)

				when := time.Unix(whenInt, 0)
				msg := proto.Message{
					To:   to,
					From: channel,
					Msg: fmt.Sprintf("Topic set by %s on %s",
						who, when.Format("2006/01/02 15:04 MST")),
				}
				sendToClient(channel, msg)

			} else if msgType == "353" {
				// list of nicknames when joining a channel
				//to := fields[2]
				channel := fields[4]
				members := fields[5:]
				members[0] = strings.TrimPrefix(members[0], ":")

				addChannelMembers(channel, members)
			} else if msgType == "354" {
				// list of users and masks when running /who
			} else if msgType == "315" {
				// end of /who list
			} else if msgType == "366" {
				to := fields[2]
				channelName := fields[3]

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
						From: from,
						Msg:  strings.Join(fields[1:], " "),
					}
					lastClient.SendMessage(&msg)
				}
			}
		}
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
	client := clientMap[clientID]
	if client != nil {
		client.SendMessage(&msg)
	} else {
		newChatIn(clientID, &msg)
	}
}

func reconnect() {
	for {
		time.Sleep(time.Second)
		_, err := login()
		if err != nil {
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

func newChatIn(clientID string, msg *proto.Message) {
	if len(clientMap) >= *chatLimit {
		fmt.Printf("Too many open chats! (%d)\nMessage: %v\n\n", len(clientMap), *msg)
		return
	}

	fd, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Panic(err)
	}

	var sock proto.Socket
	sock.SetMode(proto.ModeMsgpack)

	err = sock.UseFD(fd[0])
	if err != nil {
		log.Panic(err)
	}

	err = sock.SendHeader()
	if err != nil {
		log.Panic(err)
	}
	err = sock.SendMessage(msg)
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
				fmt.Printf("JOIN %s\n", clientID)
				fmt.Fprintf(irc, "JOIN %s\n", clientID)
			}
		}

		// the maximum command length needs to account for what the IRC server will send
		// to other clients. Full host mask, plus : and a space before PRIVMSG starts
		cmdLen := maxIRCLen - getMaskLen() - 2
		ircCmd := fmt.Sprintf("PRIVMSG %s :%s", msg.To, msg.Msg)
		for len(ircCmd) > cmdLen {
			fmt.Printf("%s\n", ircCmd[:cmdLen])
			fmt.Fprintf(irc, "%s\n", ircCmd[:cmdLen])
			ircCmd = fmt.Sprintf("PRIVMSG %s :%s", msg.To, ircCmd[cmdLen:])
		}
		fmt.Printf("%s\n", ircCmd)
		fmt.Fprintf(irc, "%s\r\n", ircCmd)

		lastClient = sock
	}, func(cmd *proto.Command) { // cmd
		log.Println(cmd)
		//server.SendCommand(cmd)

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

	for _, sock := range clientMap {
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
	irc, err = login()
	if err != nil {
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
