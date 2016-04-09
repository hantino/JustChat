package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	//"strconv"
	"sync"
	"time"

	"github.com/arcaneiceman/GoVector/govec"
)

/*
	----DECLARED TYPES----
*/

//RPC Values
type MessageService int
type NodeService int
type LBService int

//Client object
type ClientItem struct {
	Username      string
	Password      string
	CurrentServer string
	PubRPCAddr    string
	NextClient    *ClientItem
}

type ServerItem struct {
	UDP_IPPORT        string
	RPC_SERVER_IPPORT string
	RPC_CLIENT_IPPORT string
	Clients           int
	NextServer        *ServerItem
}

type LoadBalancer struct {
	Address string
	Status  string
}

type HeartBeatItem struct {
	Node      *ServerItem
	NumMissed int
	Next      *HeartBeatItem
}

/* ---------------MESSAGE TYPES-------------*/
// Struct to join chat service
type NewClientSetup struct {
	UserName   string
	Password   string
	RpcAddress string
}

//Retrun to client
type ServerReply struct {
	Message string
}

type NodeListReply struct {
	ListOfNodes *ServerItem
}

// address of chat server
type ChatServer struct {
	ServerName       string
	ServerRpcAddress string
}

//NewStorageNode Args
type NewNodeSetup struct {
	RPC_CLIENT_IPPORT string
	RPC_SERVER_IPPORT string
	UDP_IPPORT        string
}

//reply from node with message
type NodeReply struct {
	Message string
}

type LBMessage struct {
	Message      string
	OnlineNumber int
}

type LBDataReply struct {
	Clients *ClientItem
	Nodes   *ServerItem
}

type NodeToRemove struct {
	Node *ServerItem
}
type LBReply struct {
	Message string
}

type NewClientObj struct {
	ClientObject *ClientItem
}

/*
	----GLOBAL VARIABLES----
*/
//Net Info of this server
var clientConnAddress string
var nodeConnAdress string
var heartbeatAddr string
var lbDesignation int

//List of All LoadBalance Servers
var LBServers []LoadBalancer

//Lists
var clientList *ClientItem
var serverList *ServerItem
var heartsToCheck *HeartBeatItem

//List of locks
var serverListMutex sync.Mutex
var nodeConditional *sync.Cond

var clientListMutex sync.Mutex
var clientConditional *sync.Cond

// GoVector log
var Logger *govec.GoLog

func main() {

	// Parse arguments
	usage := fmt.Sprintf("Usage: %s [client ip:port] [server ip:port] [heartbeat ip:port] \n", os.Args[0])
	if len(os.Args) != 4 {
		fmt.Printf(usage)
		os.Exit(1)
	}

	clientConnAddress = os.Args[1]
	nodeConnAdress = os.Args[2]
	heartbeatAddr = os.Args[3]

	LBServers = []LoadBalancer{LoadBalancer{"127.0.0.1:10001", "offline"},
		LoadBalancer{"127.0.0.1:10002", "offline"},
		LoadBalancer{"127.0.0.1:10003", "offline"}}

	////Print out address information
	ip := GetLocalIP()
	// listen on first open port server finds
	clientServer, err := net.Listen("tcp", ip+":0")
	if err != nil {
		fmt.Println("Client Server Error:", err)
		return
	}
	defer clientServer.Close()

	ipV4 := clientServer.Addr().String()
	fmt.Print("This machine's address: " + ipV4 + "\n")

	// Create log
	Logger = govec.InitializeMutipleExecutions("lb "+ipV4, "sys")
	Logger.LogThis("LB was initialized", "lb "+ipV4, "{\"lb "+ipV4+"\":1}")

	//Locks
	serverListMutex = sync.Mutex{}
	nodeConditional = sync.NewCond(&serverListMutex)

	clientListMutex = sync.Mutex{}
	clientConditional = sync.NewCond(&clientListMutex)

	//Initialize Clientlist and serverlist
	clientList = nil
	serverList = nil

	//Startup Method to get client list and server list from existing load balancers
	initializeLB()

	//Run heartbeet check to see if nodes are still running
	go heartbeetCheck()

	//setup to accept rpcCalls on the first availible port
	clientService := new(MessageService)
	rpc.Register(clientService)

	rpcListener, err := net.Listen("tcp", clientConnAddress)
	if err != nil {
		log.Fatal("listen error:", err)
	}

	//Listener go function for clients
	go func() {
		for {
			println("Waiting for Client Calls")
			clientConnection, err := rpcListener.Accept()
			if err != nil {
				log.Fatal("Connection error:", err)
			}
			go rpc.ServeConn(clientConnection)
			Logger.LogLocalEvent("rpc client connection started")
			println("Accepted Call from " + clientConnection.RemoteAddr().String())
		}
	}()

	//Listener go function for other load balancers
	lbServ := new(LBService)
	rpc.Register(lbServ)
	lBListener, _ := net.Listen("tcp", LBServers[lbDesignation].Address)
	go func() {
		for {
			loadBalanceConnection, err := lBListener.Accept()
			if err != nil {
				log.Fatal("LoadBalancer Connection error: ", err)
			}

			go rpc.ServeConn(loadBalanceConnection)
			println("Accepted LoadBalancer Call from: " + loadBalanceConnection.RemoteAddr().String())
		}
	}()

	//setup to accept rpcCalls from message servers
	messageNodeService := new(NodeService)
	rpc.Register(messageNodeService)

	//Handle message/storage connection setup
	messageNodeListener, err := net.Listen("tcp", nodeConnAdress)
	checkError(err)

	for {
		messageNodeConn, err := messageNodeListener.Accept()
		checkError(err)
		if err != nil {
			log.Fatal("Connection error:", err)
		}
		go rpc.ServeConn(messageNodeConn)
	}
}
 
/*~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
	      LOCAL HELPER FUNCTIONS
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~*/

func deleteNodeFromList(udpAddr string) {
	//As every node is unique in its UDP address we can assume deletion after we find that address
	//and return right away

	//initialize variable
	i := serverList

	//if there are no servers, return
	//Shouldn't happen, but just in case
	if i == nil {
		return
	}
	//if i is the one we want to delete, remove it and return
	if i.UDP_IPPORT == udpAddr {
		println("Deleting: ", i.UDP_IPPORT)
		serverList = (*i).NextServer
		return
	}

	//if i is not the one we want, search until it is found
	j := (*i).NextServer

	for j != nil {
		//if found, delete
		if j.UDP_IPPORT == udpAddr {
			println("Deleting: ", i.UDP_IPPORT)
			(*i).NextServer = (*j).NextServer
			return
		}

		i = (*i).NextServer
		j = (*i).NextServer
	}

	return
}

func heartbeetCheck() {
	for {
		time.Sleep(20 * time.Millisecond)
		nodeConditional.L.Lock()

		if(serverList == nil){
			//No servers connected

		} else {
			i := serverList

			for i != nil {

				cc, err := rpc.Dial("tcp", i.RPC_SERVER_IPPORT)
				if err != nil {
					//assume node is dead
					println("He's Dead Jim!")
					deleteNodeFromList(i.UDP_IPPORT)
				} else {
					//Server Connected
					cc.Close()
				}

				i = (*i).NextServer
			}
		}
		nodeConditional.L.Unlock()
	}
}

func addLBToActiveList(i int) {
	LBServers[i].Status = "online"
}

func contactLBsToAnnounceSelf() {
	var i = 0

	for i < 3 {
		if LBServers[i].Status == "online" && i != lbDesignation {
			conn, _ := rpc.Dial("tcp", LBServers[i].Address)
			var rpcUpdateMessage LBMessage
			var lbReply LBDataReply

			rpcUpdateMessage.Message = "NIL"
			rpcUpdateMessage.OnlineNumber = lbDesignation

			conn.Call("LBService.GetCurrentData", rpcUpdateMessage, &lbReply)
		}
		i++
	}
}

func getInfoFromFirstLB() {
	var i = 0
	for i < 3 {
		if LBServers[i].Status == "online" && i != lbDesignation {
			break
		}
		i++
	}

	if i == 3 {
		println("I am the only one online")
		return
	}

	conn, err := rpc.Dial("tcp", LBServers[i].Address)
	if err != nil {
		println("Error: ", err.Error())
	}

	var rpcUpdateMessage LBMessage
	var lbReply LBDataReply

	rpcUpdateMessage.Message = "M"

	callError := conn.Call("LBService.GetCurrentData", rpcUpdateMessage, &lbReply)
	if callError != nil {
		println("Error 2: ", callError.Error())
	}


	clientList = lbReply.Clients
	serverList = lbReply.Nodes

	return
}

func initializeLB() {
	lbDesignation = -1
	var i = 0
	//check if designation already used
	for i < 3 {
		//dial and check for err
		_, err := rpc.Dial("tcp", LBServers[i].Address)
		if (err != nil) && (lbDesignation == -1) {
			lbDesignation = i
			//println("Error: ", err.Error())
			println("I am number: ", lbDesignation)
		} else if err == nil {

			println("LoadBalancer ", i, " is online")
			addLBToActiveList(i)

		}

		i++
	}

	//If all load balancer spots are taken, shut down
	//There can't be more than 3
	if lbDesignation == -1 {
		println("3 Load Balancers Running. \n No More Needed.\nShutting Down....")
		os.Exit(2)
	}

	getInfoFromFirstLB()
	contactLBsToAnnounceSelf()

	return
}


func sendClientDataToAllLBs(c *ClientItem){
	i := 0
	for(i < 3){
		println("I: ",i)
		if(LBServers[i].Status == "online" && i != lbDesignation){
			println("Sending client to: ", i)

			conn, err := rpc.Dial("tcp", LBServers[i].Address)
			if err != nil {
				println("Error: ", err.Error())
			}

			var nC NewClientObj
			var lbReply NodeListReply

			nC.ClientObject = c

			callError := conn.Call("LBService.NewClient", nC, &lbReply)
			if callError != nil {
				println("Error 2: ", callError.Error())
			}
		}

		i++
	}

	return
}

func addClientToList(username string, password string, addr string) {

	newClient := &ClientItem{username, password, "CurrentServer", addr, nil}

	sendClientDataToAllLBs(newClient)

	if clientList == nil {
		clientList = newClient
	} else {
		newClient.NextClient = clientList
		clientList = newClient
	}

	printOutAllClients()
	return
}

//return selectedServer, error
func getServerForCLient() (*ServerItem, error) {
	//get the server with fewest clients connected to it
	next := serverList

	println("about to lock NodeCond")
	nodeConditional.L.Lock()

	for serverList == nil {
		println("Waiting")
		nodeConditional.Wait()
		println("Signaled")
	}

	next = serverList

	lowestNumberServer := serverList

	for (*next).NextServer != nil {

		if next.Clients > (*next).NextServer.Clients {
			lowestNumberServer = (*next).NextServer
		}

		next = (*next).NextServer
	}

	nodeConditional.L.Unlock()

	if lowestNumberServer != nil {
		return lowestNumberServer, nil
	} else {
		return nil, errors.New("No Connected Servers")
	}
}

func authenticationFailure(username string, password string, pubAddr string) bool {

	next := clientList

	//check to see if username exists
	for next != nil {
		if (*next).Username == username {
			if (*next).Password == password {
				//username match and password match
				return false
			}
			//username exists but password doesn't match
			return true
		}
		next = (*next).NextClient
	}

	//if username doesnt exist, add to list
	addClientToList(username, password, pubAddr)

	return false
}

func addClient(newClient *ClientItem) {
	if clientList == nil {
		clientList = newClient
	} else {
		newClient.NextClient = clientList
		clientList = newClient
	}
}

func addNode(udp string, clientRPC string, serverRPC string, broadcast bool) {

	//TODO: need restart implementation
	newNode := &ServerItem{udp, clientRPC, serverRPC, 0, nil}

	println("\n\nNew Node\n-------------")
	println(newNode.UDP_IPPORT)
	println(newNode.RPC_SERVER_IPPORT)
	println(newNode.RPC_CLIENT_IPPORT)
	println(newNode.Clients)

	if serverList == nil {
		serverList = newNode
	} else {
		newNode.NextServer = serverList
		serverList = newNode
	}

	//alert all nodes to the new node
	allertAllNodes(newNode)
	//alert other online load balancers
	if broadcast {
		alertAllLoabBalancers(newNode)
	}

	return
}

func alertAllLoabBalancers(newNode *ServerItem) {
	var nodeSetupMessage NewNodeSetup
	nodeSetupMessage.RPC_CLIENT_IPPORT = newNode.RPC_CLIENT_IPPORT
	nodeSetupMessage.RPC_SERVER_IPPORT = newNode.RPC_SERVER_IPPORT
	nodeSetupMessage.UDP_IPPORT = newNode.UDP_IPPORT

	var replyFromNode NodeListReply

	//iterate through all loadbalancers and alert them to the new node
	var i = 0
	for i < 3 {
		if LBServers[i].Status == "online" && i != lbDesignation {
			conn, err := rpc.Dial("tcp", LBServers[i].Address)
			if err == nil {
				conn.Call("LBService.NewNode", nodeSetupMessage, &replyFromNode)
			} else {
				LBServers[i].Status = "offline"
			}
		}
		i++
	}

	return

}

func allertAllNodes(newNode *ServerItem) {
	//dial all active nodes and alert them of the new node in the system
	next := serverList
	for next != nil {
		conn, err := rpc.Dial("tcp", next.RPC_SERVER_IPPORT)
		if err != nil {
			println("Error dialing node w/UDP info of: ", next.UDP_IPPORT)
			return
		}

		var nodeSetupMessage NewNodeSetup
		nodeSetupMessage.RPC_CLIENT_IPPORT = newNode.RPC_CLIENT_IPPORT
		nodeSetupMessage.RPC_SERVER_IPPORT = newNode.RPC_SERVER_IPPORT
		nodeSetupMessage.UDP_IPPORT = newNode.UDP_IPPORT

		var replyFromNode ServerReply

		callErr := conn.Call("NodeService.NewStorageNode", nodeSetupMessage, &replyFromNode)
		if callErr != nil {
			println("Error with method call 'NodeService.NewStorageNode' of: ", next.UDP_IPPORT)
			return
		}

		next = (*next).NextServer

	}
}

func isNewNode(ident string) bool {
	next := serverList

	for next != nil {
		if (*next).UDP_IPPORT == ident {
			return false
		}
		next = (*next).NextServer
	}

	return true
}

/**************************************************
	RPC METHODS FOR LOAD BALANCERS
*****************************************************/
func (lbSvc *LBService) NewNode(message *NewNodeSetup, reply *NodeListReply) error {
	
	nodeConditional.L.Lock()
	if isNewNode(message.UDP_IPPORT) {
		addNode(message.UDP_IPPORT, message.RPC_CLIENT_IPPORT, message.RPC_SERVER_IPPORT, false)
	}

	nodeConditional.L.Unlock()
	nodeConditional.Signal()

	return nil
}

func (lbSvc *LBService) NewClient(message *NewClientObj, reply *NodeListReply) error {
	clientConditional.L.Lock()

	addClient(message.ClientObject)

	clientConditional.L.Unlock()
	clientConditional.Signal()


	return nil
}

func (lbSvc *LBService) GetCurrentData(message *LBMessage, reply *LBDataReply) error {


	if message.Message != "NIL" {

		clientConditional.L.Lock()
		nodeConditional.L.Lock()

		println(message.OnlineNumber)
		LBServers[message.OnlineNumber].Status = "online"


		reply.Clients = clientList
		reply.Nodes = serverList


		nodeConditional.L.Unlock()
		clientConditional.L.Unlock()
	} else {
		println("New LB is online: ", message.OnlineNumber)
		LBServers[message.OnlineNumber].Status = "online"

		println("Status of ",message.OnlineNumber," is ",LBServers[message.OnlineNumber].Status)
	}

	return nil
}

/*************************************
	RPC METHODS FOR NODES
***************************************/

//Function a node will call when it comes online
func (nodeSvc *NodeService) NewNode(message *NewNodeSetup, reply *NodeListReply) error {
	//add node to list on connection

	nodeConditional.L.Lock()

	println("A new node is trying to connect", message.UDP_IPPORT)
	if isNewNode(message.UDP_IPPORT) {
		addNode(message.UDP_IPPORT, message.RPC_CLIENT_IPPORT, message.RPC_SERVER_IPPORT, true)
	}

	newNode := NewNodeSetup{
		RPC_CLIENT_IPPORT: message.RPC_CLIENT_IPPORT,
		RPC_SERVER_IPPORT: message.RPC_SERVER_IPPORT,
		UDP_IPPORT:        message.UDP_IPPORT}

	notifyServersOfNewNode(newNode)
	reply.ListOfNodes = serverList
	Logger.LogLocalEvent("new storage node online")

	nodeConditional.L.Unlock()
	nodeConditional.Signal()

	return nil
}

type MessageObj struct {
	Message string
}

func (nodeSvc *NodeService) GetClientAddr(uname *MessageObj, addr *MessageObj) error {
	clientConditional.L.Lock()

	username := uname.Message
	i := clientList

	for i != nil {
		if i.Username == username {
			addr.Message = i.PubRPCAddr
			clientConditional.L.Unlock()
			return nil
		}

		i = (*i).NextClient
	}

	clientConditional.L.Unlock()
	return nil
}

/*****************************************
	RPC METHODS FOR CLIENTS
******************************************/

//Function for receiving a message from a client
func (msgSvc *MessageService) JoinChatService(message *NewClientSetup, reply *ServerReply) error {

	// if user name not taken, server dials RPC address in message.RPCAddress
	// and updates client with new rpc address, then replies WELCOME
	// unless there is error dialing RPC to client then replies DIAL-ERROR
	// otherwise, server replies, USERNAME-TAKEN

	//check username, if taken reply username taken
	if authenticationFailure(message.UserName, message.Password, message.RpcAddress) {
		reply.Message = "USERNAME-TAKEN"
		//else dial rpc

	} else {

		clientConn, err := rpc.Dial("tcp", message.RpcAddress)
		if err != nil {
			reply.Message = "DIAL-ERROR"
			return nil
		}

		var clientReply ServerReply
		var rpcUpdateMessage ChatServer

		//Dial and update the client with their server address
		println("Getting server for client")
		selectedServer, selectionError := getServerForCLient()
		if selectionError != nil {
			println(selectionError.Error())
		}

		rpcUpdateMessage.ServerName = "Server X"
		rpcUpdateMessage.ServerRpcAddress = selectedServer.RPC_CLIENT_IPPORT

		callErr := clientConn.Call("ClientMessageService.UpdateRpcChatServer", rpcUpdateMessage, &clientReply)
		if callErr != nil {
			reply.Message = "DIAL-ERROR"
			return nil
		}

		Logger.LogLocalEvent("client joined chat service successful")
		reply.Message = "WELCOME"
	}

	return nil
}

/*******************************************
	CHECK for ERRORS
**********************************************/
func checkError(err error) {
	if err != nil {
		log.Fatal(os.Stderr, "Error ", err.Error())
		os.Exit(1)
	}
}

/* Get local IP */
// GetLocalIP returns the non loopback local IP of the host
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		// check the address type and if it is not a loopback the display it
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func printOutAllClients() {
	//print list of clients
	toPrint := clientList
	println(" ")
	println("List of Clients")
	println("---------------")
	for toPrint != nil {
		fmt.Print((*toPrint).Username)
		toPrint = (*toPrint).NextClient
	}


	return
}

func notifyServersOfNewNode(newNode NewNodeSetup) {

	next := serverList

	for next != nil {
		systemService, err := rpc.Dial("tcp", (*next).RPC_SERVER_IPPORT)
		//checkError(err)
		if err != nil {
			println("Notfifying nodes of new node: Node ", (*next).UDP_IPPORT, " isn't accepting tcp conns so skip it...")
			//it's dead but the ping will eventually take care of it
		} else {
			var reply ServerReply

			err = systemService.Call("NodeService.NewStorageNode", newNode, &reply)
			checkError(err)
			if err == nil {
				fmt.Println("we received a reply from the server: ", reply.Message)
			}
			systemService.Close()
		}
		next = (*next).NextServer
	}
}