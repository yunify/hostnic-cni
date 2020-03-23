package types

var StopCh chan struct {}

var IpsetCh chan IpsetVxnet

var NodeNotify chan string


func initChannel() {
	StopCh = make(chan struct{})
	NodeNotify = make(chan string)
	IpsetCh = make(chan IpsetVxnet)
}