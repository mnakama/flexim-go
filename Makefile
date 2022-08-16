BUILD = go build

.PHONY : all
all : flexim-chat flexim-listener flexim-client irc-client

flexim-chat : chat.go proto/proto.go
	$(BUILD) -o flexim-chat chat.go

flexim-listener : listener.go
	$(BUILD) -o flexim-listener listener.go

flexim-client : client.go proto/proto.go
	$(BUILD) -o flexim-client client.go

irc-client : irc-client.go proto/proto.go
	$(BUILD) -o irc-client irc-client.go


.PHONY : clean
clean :
	rm flexim-chat flexim-listener flexim-client irc-client
