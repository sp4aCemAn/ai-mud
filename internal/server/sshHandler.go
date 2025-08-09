package server

import (
	"context"
	"net"

	util "github.com/sp4aCemAn/ai-mud/internal/util"
)


func CreateListiner(ctx context.Context) (net.Listener, error){ 
	listener, err := net.Listen("tcp", "0.0.0.0:2222")
	if err != nil {
		util.WarningHandler(err, "error while setting up listener")
		return nil, err
	}
	return listener, nil
}

func AcceptConn (listener net.Listener, ctx context.Context) (net.Conn, error){
	

	return nil, nil
}






