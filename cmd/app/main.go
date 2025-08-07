package main
import (
	"fmt"
	"context"
	

)

func main(){
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	<- ctx.Done()

}
