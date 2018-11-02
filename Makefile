CC = go build

.PHONY : all
all : flexim-chat flexim-listener flexim-client

flexim-chat : chat.go
	$(CC) -o flexim-chat chat.go

flexim-listener : listener.go
	$(CC) -o flexim-listener listener.go

flexim-client : client.go
	$(CC) -o flexim-client client.go


.PHONY : clean
clean :
	rm flexim-chat flexim-listener flexim-client
