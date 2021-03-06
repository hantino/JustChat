package main

//**************************************************************************
//
//                           IMPORT STATEMENT
//
//**************************************************************************

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"

	"github.com/arcaneiceman/GoVector/govec"
)

//**************************************************************************
//
//                  DECLARED TYPES AND GLOBAL VARIABLES
//
//**************************************************************************

//RPC Service Values
type MessageService int
type NodeService int
type LBService int

//Client object
type ClientItem struct {
	Username      string
	Password      string
	CurrentServer string //UDP of server attached to
	PubRPCAddr    string
	NextClient    *ClientItem
}

//Server Object
type ServerItem struct {
	UDP_IPPORT        string
	RPC_SERVER_IPPORT string
	RPC_CLIENT_IPPORT string
	Clients           int
	NextServer        *ServerItem
}

//LoadBalancer Object
type LoadBalancer struct {
	Address string
	Status  string
}

//
//    RPC MESSAGE TYPES
//

// Struct to join chat service
type NewClientSetup struct {
	Username   string
	Password   string
	RpcAddress string
}

// Return to client
type ServerReply struct {
	Message string
}

type NodeListReply struct {
	ListOfNodes *ServerItem
}

// Address of server
type ChatServer struct {
	ServerName       string
	ServerRpcAddress string
}

// NewStorageNode Args
type NewNodeSetup struct {
	RPC_CLIENT_IPPORT string
	RPC_SERVER_IPPORT string
	UDP_IPPORT        string
}

// Reply from node with message
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

type ClientRequest struct {
	Username string
}

//
//    GLOBAL VARIABLES
//

//Net Info of this server
var clientConnAddress string
var nodeConnAdress string
var lbDesignation int

// List of All LoadBalancer Servers
var LBServers []LoadBalancer

// Lists
var clientList *ClientItem
var serverList *ServerItem
var globalFileList []string

// List of locks
var serverListMutex sync.Mutex
var nodeConditional *sync.Cond

var clientListMutex sync.Mutex
var clientConditional *sync.Cond

var fileListMutex sync.Mutex
var filesCond *sync.Cond

// GoVector log
var Logger *govec.GoLog
var lbId string

//**************************************************************************
//
//                             MAIN METHOD
//
//**************************************************************************

func main() {

	// Parse arguments
	usage := fmt.Sprintf("Usage: %s [client ip:port1] [server ip:port2] \n", os.Args[0])
	if len(os.Args) != 3 {
		fmt.Printf(usage)
		os.Exit(1)
	}

	//assign argument values to global variables
	clientConnAddress = os.Args[1]
	nodeConnAdress = os.Args[2]

	LBServers = []LoadBalancer{LoadBalancer{"127.0.0.1:10001", "offline"},
		LoadBalancer{"127.0.0.1:10002", "offline"},
		LoadBalancer{"127.0.0.1:10003", "offline"}}

	globalFileList = []string{}

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
	lbId = ipV4[5:]
	Logger = govec.Initialize("lb "+lbId, "lb "+lbId)

	//Locks
	serverListMutex = sync.Mutex{}
	nodeConditional = sync.NewCond(&serverListMutex)

	clientListMutex = sync.Mutex{}
	clientConditional = sync.NewCond(&clientListMutex)

	fileListMutex = sync.Mutex{}
	filesCond = sync.NewCond(&fileListMutex)

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

	//Listener go function for clients
	rpcListener, err := net.Listen("tcp", clientConnAddress)
	if err != nil {
		log.Fatal("listen error:", err)
	}
	go func() {
		for {
			println("Waiting for Client Calls")
			clientConnection, err := rpcListener.Accept()
			if err != nil {
				log.Fatal("Connection error:", err)
			}
			go rpc.ServeConn(clientConnection)
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

	//setup to accept rpcCalls from servers
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

//**************************************************************************
//
//                        LOCAL HELPER FUNCTIONS
//
//**************************************************************************

//	~~~deleteNodeFromList~~~
//
//	Removes a ServerItem from serverList if it's UDP_IPPORT
//	matches the udpAddr argument
//
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

//	~~~heartbeetCheck~~~
//
//	Checks every 20 milliseconds to see if any servers
//	have died by attempting to dial their RPC listening port
//
//	If they have been declared dead, all clients on that message
//	server are told to use a different one
//
func heartbeetCheck() {
	for {
		time.Sleep(20 * time.Millisecond)
		nodeConditional.L.Lock()

		if serverList == nil {
			//No servers connected
		} else {
			i := serverList

			for i != nil {
				cc, err := rpc.Dial("tcp", i.RPC_SERVER_IPPORT)
				if err != nil {
					//assume node is dead
					println("He's Dead Jim!")
					servA := i.UDP_IPPORT
					deleteNodeFromList(servA)

					go giveClientNewServer(servA)
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

//	~~~giveClientNewServer~~~
//
//	Every client whose CurrentServer value matches the serverAddr
//	argument will be told to use a different server
//
func giveClientNewServer(serverAddr string) {
	println("Giving new server")
	i := clientList
	println(i.CurrentServer)
	println(serverAddr)

	for i != nil {
		if i.CurrentServer == serverAddr {
			println("MATCH")

			go func(val string, name string) {
				println("About tl lock")

				nodeConditional.L.Lock()
				for serverList == nil {
					nodeConditional.Wait()
				}
				println("Done waiting")

				s, _ := getServerForCLient()

				nodeConditional.L.Unlock()
				nodeConditional.Signal()

				clientConn, err := rpc.Dial("tcp", val)
				if err != nil {
					println("Error changing messaging server. Client doesn't have good node")
					return
				}

				var clientReply ServerReply
				var rpcUpdateMessage ChatServer

				rpcUpdateMessage.ServerName = "Server N"
				rpcUpdateMessage.ServerRpcAddress = s.RPC_CLIENT_IPPORT

				addServerDataToClient(s.UDP_IPPORT, name)

				println("Assigning Client New Node")
				outbuf := Logger.PrepareSend("updating client w/ new server", rpcUpdateMessage)
				callErr := clientConn.Call("ClientMessageService.UpdateRpcChatServer", outbuf, &clientReply)
				if callErr != nil {
					println("Error changing messaging server. Client doesn't have good node")
					return
				}
			}(i.PubRPCAddr, i.Username)

		}
		i = (*i).NextClient
	}

	if i == nil {
		return
	}
	return
}

//	~~~addLBToActiveList~~~
//
//	This method will set a load balancer with the value i to be
//	"online"
//
func addLBToActiveList(i int) {
	LBServers[i].Status = "online"
	return
}

//	~~~contactLBsToAnnounceSelf~~~
//
//	This method alerts all other online LoadBalancers that this load
//	balancer is now online.
//
func contactLBsToAnnounceSelf() {
	for i := 0; i < 3; i++ {
		if LBServers[i].Status == "online" && i != lbDesignation {
			conn, _ := rpc.Dial("tcp", LBServers[i].Address)
			var rpcUpdateMessage LBMessage
			var lbReply LBDataReply

			rpcUpdateMessage.Message = "NIL"
			rpcUpdateMessage.OnlineNumber = lbDesignation

			outbuf := Logger.PrepareSend("announce self to a loadbalancer", rpcUpdateMessage)
			conn.Call("LBService.GetCurrentData", outbuf, &lbReply)
		}
	}
}

//	~~~getInfoFromFirstLB~~~
//
//	This method will find the first "online" load balancer and get the most
//	up to date client list and server list
//
//	If this is the first load balancer to start, no data is retrieved
//
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

	outbuf := Logger.PrepareSend("request current data from other lb", rpcUpdateMessage)
	callError := conn.Call("LBService.GetCurrentData", outbuf, &lbReply)

	if callError != nil {
		println("Error 2: ", callError.Error())
	}

	clientList = lbReply.Clients
	serverList = lbReply.Nodes
	Logger.LogLocalEvent("received current data from other lb")

	return
}

//	~~~initializeLB~~~
//
//	This method is called at startup to prepare this load balancer for use
//
//	If there are already 3 load balancers online, then this one will shut down

//
func initializeLB() {
	lbDesignation = -1

	//check if designation already used
	for i := 0; i < 3; i++ {
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

//	~~~updateClientDataToAllLBs~~~
//
//	Updates the client data upon client reconnect to all load balancers
//	currently online.
//
func updateClientDataToAllLBs(c *ClientItem) {
	for i := 0; i < 3; i++ {
		println("I: ", i)
		if LBServers[i].Status == "online" && i != lbDesignation {
			println("updating client data on: ", i)

			conn, err := rpc.Dial("tcp", LBServers[i].Address)
			if err != nil {
				println("Error: ", err.Error())
			}

			var nC NewClientObj
			var lbReply NodeListReply

			nC.ClientObject = c

			outbuf := Logger.PrepareSend("push client update", nC)
			callError := conn.Call("LBService.UpdateClient", outbuf, &lbReply)
			if callError != nil {
				println("Error 2: ", callError.Error())
			}
		}
	}
	return
}

//	~~~sendClientDataToAllLBs~~~
//
//	Sends the ClientItem specified to all of the online loadbalancers
//
func sendClientDataToAllLBs(c *ClientItem) {
	for i := 0; i < 3; i++ {
		println("I: ", i)
		if LBServers[i].Status == "online" && i != lbDesignation {
			println("Sending client to: ", i)

			conn, err := rpc.Dial("tcp", LBServers[i].Address)
			if err != nil {
				println("Error: ", err.Error())
			}

			var nC NewClientObj
			var lbReply NodeListReply

			nC.ClientObject = c

			outbuf := Logger.PrepareSend("send client data", nC)
			callError := conn.Call("LBService.NewClient", outbuf, &lbReply)
			if callError != nil {
				println("Error 2: ", callError.Error())
			}
		}
	}
	return
}

//	~~~addClientToList~~~
//
//	Creats a new ClientItem object and adds it to the clientList
//
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

//	~~~getServerForCLient~~~
//
//	Returns the server for the client with the fewest connected clients
//	and increments the number of connected clients on that server by 1
//
func getServerForCLient() (*ServerItem, error) {
	//get the server with fewest clients connected to it
	next := serverList

	next = serverList

	lowestNumberServer := serverList

	for (*next).NextServer != nil {

		if next.Clients > (*next).NextServer.Clients {
			lowestNumberServer = (*next).NextServer
		}

		next = (*next).NextServer
	}

	if lowestNumberServer != nil {
		lowestNumberServer.Clients++
		return lowestNumberServer, nil
	} else {
		return nil, errors.New("No Connected Servers")
	}
}

//	~~~authenticationFailure~~~
//
//	Checks to see if the username and password are correct. If the username hasn't
//	been used before then a new ClientItem is created and added to the list and
//	false is returned.
//
//	If the username has been used and the password is not correct then true is returned
//
func authenticationFailure(username string, password string, pubAddr string) bool {

	next := clientList

	//check to see if username exists
	for next != nil {
		if (*next).Username == username {
			if (*next).Password == password {
				//username match and password match
				// update client with new rpc address
				(*next).PubRPCAddr = pubAddr
				updateClient := &ClientItem{username, password, "CurrentServer", pubAddr, nil}
				updateClientDataToAllLBs(updateClient)

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

//	~~~updateClientInfo~~~
//
//	Compares the username in the newClient to that of all ClientItems in the
//	clientList and will update the client in the clientList with the information
//	if there is a match
//
func updateClientInfo(newClient *ClientItem) {
	next := clientList

	for next != nil {
		if (*next).Username == newClient.Username {
			(*next).PubRPCAddr = newClient.PubRPCAddr
			break
		}
		next = (*next).NextClient
	}
	return
}

//	~~~addClient~~~
//
//	Adds the newClient as specified in the argument to the front of the clientList
//
func addClient(newClient *ClientItem) {
	if clientList == nil {
		clientList = newClient
	} else {
		newClient.NextClient = clientList
		clientList = newClient
	}
}

//	~~~addNode~~~
//
//	Addes a new server to the serverList by creating a ServerItem with
//	the information specified in the arguments
//
func addNode(udp string, clientRPC string, serverRPC string, broadcast bool) {
	//create the ServerItem object
	newNode := &ServerItem{udp, clientRPC, serverRPC, 0, nil}

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

//	~~~alertAllLoabBalancers~~~
//
//	Iterate through all online load balancers and alert them to the presence of a new
//	server
//
//	This also checks to see if a load balancer has gone offline and sets it to "offline"
//	as needed
//
func alertAllLoabBalancers(newNode *ServerItem) {
	var nodeSetupMessage NewNodeSetup
	nodeSetupMessage.RPC_CLIENT_IPPORT = newNode.RPC_CLIENT_IPPORT
	nodeSetupMessage.RPC_SERVER_IPPORT = newNode.RPC_SERVER_IPPORT
	nodeSetupMessage.UDP_IPPORT = newNode.UDP_IPPORT

	var replyFromNode NodeListReply

	//iterate through all loadbalancers and alert them to the new node
	for i := 0; i < 3; i++ {
		if LBServers[i].Status == "online" && i != lbDesignation {
			conn, err := rpc.Dial("tcp", LBServers[i].Address)
			if err == nil {
				outbuf := Logger.PrepareSend("new server alert for lb", nodeSetupMessage)
				conn.Call("LBService.NewNode", outbuf, &replyFromNode)
			} else {
				LBServers[i].Status = "offline"
			}
		}
	}

	return
}

//	~~~allertAllNodes~~~
//
//	Iterate through all servers and alert them to the presence of a new
//	server
//
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

		outbuf := Logger.PrepareSend("new server alert for other servers", nodeSetupMessage)
		callErr := conn.Call("NodeService.NewStorageNode", outbuf, &replyFromNode)
		if callErr != nil {
			println("Error with method call 'NodeService.NewStorageNode' of: ", next.UDP_IPPORT)
			return
		}

		next = (*next).NextServer
	}
}

//	~~~isNewNode~~~
//
//	Check to see if a node with the UDP_IPPORT is already added to the serverList
//	return false if it is
//	return true if the node is new
//
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

//	~~~addServerDataToClient~~~
//
//	Update the current server of the client to reflect the value of addrInfo
//
func addServerDataToClient(addrInfo string, clientUname string) {
	i := clientList
	for i != nil {
		if i.Username == clientUname {
			i.CurrentServer = addrInfo
			return
		}

		i = (*i).NextClient
	}

	return
}

//	~~~checkError~~~
//
//	Generic check for error that terminates execution
//
func checkError(err error) {
	if err != nil {
		log.Fatal(os.Stderr, "Error ", err.Error())
		os.Exit(1)
	}
}

//	~~~GetLocalIP~~~
//
//	Get this load balancer's IP address
//
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

//	~~~printOutAllClients~~~
//
//	Print the current list of clients to console
//
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

//	~~~notifyServersOfNewNode~~~
//
//	Will iterate over the entire server list and allert all servers
//	to the presence of a new server.
//
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

			outbuf := Logger.PrepareSend("notification: new server node online", newNode)
			err = systemService.Call("NodeService.NewStorageNode", outbuf, &reply)
			checkError(err)
			if err == nil {
				fmt.Println("we received a reply from the server: ", reply.Message)
			}
			systemService.Close()
		}
		next = (*next).NextServer
	}
}

//**************************************************************************
//
//                     RPC METHODS FOR LOAD BALANCERS
//
//**************************************************************************

//	~~~NewNode~~~
//
//	This method is called by another load balancer to alert this load balancer
//	of the addition of a server to the system.
//
func (lbSvc *LBService) NewNode(inbuf []byte, /* message *NewNodeSetup,*/ reply *NodeListReply) error {
	var message = new(NewNodeSetup)
	Logger.UnpackReceive("received new load balancer", inbuf, message)

	nodeConditional.L.Lock()
	if isNewNode(message.UDP_IPPORT) {
		addNode(message.UDP_IPPORT, message.RPC_CLIENT_IPPORT, message.RPC_SERVER_IPPORT, false)
	}

	nodeConditional.L.Unlock()
	nodeConditional.Signal()

	return nil
}

//	~~~UpdateClient~~~
//
//	This method updates the clientlist to reflect changes when a client reconnects
//	with the same username and password. The message will contain the username of the client
//	and the value that the RPCAddress should be updated to
//
func (lbSvc *LBService) UpdateClient(inbuf []byte, /* message *NewClientObj,*/ reply *NodeListReply) error {
	var message = new(NewClientObj)
	Logger.UnpackReceive("received lb client update", inbuf, message)

	clientConditional.L.Lock()

	updateClientInfo(message.ClientObject)
	clientConditional.L.Unlock()
	clientConditional.Signal()
	return nil
}

//	~~~NewClient~~~
//
//	This method is called by another load balancer to alert this load balancer
//	of the addition of a client to the system.
//
func (lbSvc *LBService) NewClient(inbuf []byte, /* message *NewClientObj,*/ reply *NodeListReply) error {
	var message = new(NewClientObj)
	Logger.UnpackReceive("received new client", inbuf, &message)

	clientConditional.L.Lock()

	addClient(message.ClientObject)

	clientConditional.L.Unlock()
	clientConditional.Signal()

	return nil
}

//	~~~GetCurrentData~~~
//
//	This method alerts this load balancer that another one is online
//	and replys back with the current list of clients and server nodes
//	in the system
//
func (lbSvc *LBService) GetCurrentData(inbuf []byte, /* message *LBMessage,*/ reply *LBDataReply) error {
	var message = new(LBMessage)
	Logger.UnpackReceive("received request for current lb data", inbuf, &message)

	if message.Message != "NIL" {
		clientConditional.L.Lock()
		nodeConditional.L.Lock()

		println(message.OnlineNumber)
		LBServers[message.OnlineNumber].Status = "online"

		reply.Clients = clientList
		reply.Nodes = serverList

		nodeConditional.L.Unlock()
		clientConditional.L.Unlock()

		Logger.LogLocalEvent("sent current data back to lb")
	} else {
		println("New LB is online: ", message.OnlineNumber)
		LBServers[message.OnlineNumber].Status = "online"

		println("Status of ", message.OnlineNumber, " is ", LBServers[message.OnlineNumber].Status)
	}

	return nil
}

//**************************************************************************
//
//                           RPC METHODS FOR NODES
//
//**************************************************************************

//	~~~NewFIle~~~
//
//	Will add the file to the list of globally availible files and return "SUCCESS" upon completion.
//
func (nodeSvc *NodeService) NewFile(inbuf []byte, /*filename *string,*/ reply *string) error {
	var filename = new(string)

	filesCond.L.Lock()
	Logger.UnpackReceive("new file added to file list", inbuf, &filename)

	globalFileList = append(globalFileList, *filename)	
	filesCond.L.Unlock()
	(*reply) = "SUCCESS"
	return nil
}

//	~~~NewNode~~~
//
//	The first call a new messaging server will make when it comes online. This method will
//	add the messaging server to the list ofavailible servers and will signal to any waiting
//	routines that there is a new server added. All loadbalancers are alerted to the additional
//	messaging server.
//
func (nodeSvc *NodeService) NewNode(inbuf []byte, /*message *NewNodeSetup,*/ reply *NodeListReply) error {
	var message = new(NewNodeSetup)
	Logger.UnpackReceive("received new node connection", inbuf, &message)

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

	nodeConditional.L.Unlock()
	nodeConditional.Signal()

	return nil
}

//	~~~GetClientAddr~~~
//
//	Given the username of the client in a ClientRequest message, the loadbalancer will return
//	the public address that RPC calls can be received on for that client.
//
func (nodeSvc *NodeService) GetClientAddr(inbuf []byte, /*uname *ClientRequest, */addr *ServerReply) error {
	var uname = new(ClientRequest)
	Logger.UnpackReceive("received request for client address", inbuf, &uname)

	clientConditional.L.Lock()

	username := uname.Username
	i := clientList

	for i != nil {
		if i.Username == username {
			addr.Message = i.PubRPCAddr
			clientConditional.L.Unlock()
			Logger.LogLocalEvent("return client address")
			return nil
		}

		i = (*i).NextClient
	}

	clientConditional.L.Unlock()
	return nil
}

//**************************************************************************
//
//                         RPC METHODS FOR CLIENTS
//
//**************************************************************************

//	~~~JoinChatService~~~
//
//	This is the first method a client calls when it comes online. It will add
//	the client to the list of clients after authenticating username and password.
//	Then it will find a server to use and tell the client to connect to it.
//
//	Calls are made to other load balancers to alert them to the changes.
//
func (msgSvc *MessageService) JoinChatService(inbuf []byte, /*message *NewClientSetup,*/ reply *ServerReply) error {

	// if user name not taken, server dials RPC address in message.RPCAddress
	// and updates client with new rpc address, then replies WELCOME
	// unless there is error dialing RPC to client then replies DIAL-ERROR
	// otherwise, server replies, USERNAME-TAKEN

	var message = new(NewClientSetup)
	Logger.UnpackReceive("received request to join chat service", inbuf, &message)

	//check username, if taken reply username taken
	//else dial rpc
	if authenticationFailure(message.Username, message.Password, message.RpcAddress) {
		reply.Message = "USERNAME-TAKEN"

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
		nodeConditional.L.Lock()

		for serverList == nil {
			nodeConditional.Wait()
		}

		println("Selecting server")
		selectedServer, selectionError := getServerForCLient()
		nodeConditional.L.Unlock()
		nodeConditional.Signal()

		if selectionError != nil {
			println(selectionError.Error())
		}

		addServerDataToClient(selectedServer.UDP_IPPORT, message.Username)

		rpcUpdateMessage.ServerName = "Server X"
		rpcUpdateMessage.ServerRpcAddress = selectedServer.RPC_CLIENT_IPPORT
		println(rpcUpdateMessage.ServerRpcAddress)

		outbuf := Logger.PrepareSend("update client rpc server", rpcUpdateMessage)
		callErr := clientConn.Call("ClientMessageService.UpdateRpcChatServer", outbuf, &clientReply)
		if callErr != nil {
			reply.Message = "DIAL-ERROR"
			return nil
		}

		reply.Message = "WELCOME"
		Logger.LogLocalEvent("client joined chat service successful")
	}

	return nil
}

//	~~~NewFIle~~~
//
//	This returns the list of all availible files to a client
//
func (msgSvc *MessageService) GetFileList(inbuf []byte, /*message *string,*/ reply *([]string)) error {
	var message = new(string)
	Logger.UnpackReceive("file list requested", inbuf, &message)

	filesCond.L.Lock()
	(*reply) = globalFileList
	filesCond.L.Unlock()
	
	Logger.LogLocalEvent("returned file list")
	return nil
}
