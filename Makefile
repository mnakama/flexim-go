CC = go build

.PHONY : all
all : flexim-chat flexim-listener

flexim-chat : chat.go
	$(CC) -o flexim-chat chat.go

flexim-listener : listener.go
	$(CC) -o flexim-listener listener.go


.PHONY : clean
clean :
	rm flexim-chat flexim-listener
