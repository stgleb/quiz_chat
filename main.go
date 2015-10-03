package main

import (
    "html/template"
    "log"
    "net/http"
    "github.com/gorilla/websocket"
    "crypto/rand"
    "fmt"
    "github.com/gorilla/sessions"
    "github.com/gorilla/securecookie"
)

const (
    ADDR string = ":9000"
)

var store = sessions.NewCookieStore([]byte(
                                    securecookie.GenerateRandomKey(256)))
var freeRooms = make(map[string]*room)
var allRooms = make(map[string]*room)
var roomsCount int
var roomCapacity int

/*
    Structures used
*/

type room struct {
    name string

    // Registered connections.
    playerConns map[*playerConn]bool

    // Update state for all conn.
    updateAll chan bool

    // Register requests from the connections.
    joinChannel chan *playerConn

    // Unregister requests from connections.
    leaveChannel chan *playerConn

    //Channel for transferring questions
    questionChannel chan Question
}

type Player struct {
    name  string
}

type playerConn struct {
    ws *websocket.Conn
    player *Player
    room *room
    question string
}

type Message struct{
    message string
}

type Question struct {
    question string
    answer string
    playerName string
}
/*
    Utility function generates random id.
*/

func RandString(n int) string {
    const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
    var bytes = make([]byte, n)
    rand.Read(bytes)
    for i, b := range bytes {
        bytes[i] = alphanum[b % byte(len(alphanum))]
    }
    return string(bytes)
}

/*
    Structures methods
*/

func (pc *playerConn) sendState() {
    go func() {
        msg := "msg"
        fmt.Println("Send message")
        err := pc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
        if err != nil {
            fmt.Println("Leave due to error")
            pc.room.leaveChannel <- pc
            pc.ws.Close()
        }
    }()
}

func (r *room) updateAllPlayers() {
    for c := range r.playerConns {
        c.sendState()
    }
}

// Run the room in goroutine
func (r *room) run() {
    fmt.Println("Room running")
    for {
        select {
        case c := <-r.joinChannel:
            r.playerConns[c] = true
//            r.updateAllPlayers()
            // if room is full - delete from freeRooms
            fmt.Println("Join channel")
            fmt.Println(len(r.playerConns))
            fmt.Println(roomCapacity)

            if len(r.playerConns) == roomCapacity {
                fmt.Println("Room ", r.name, " is full")
                for pc, _ := range r.playerConns {
                    fmt.Println("Send acknowledgement to player ", pc.player.name)

                    msg := "full"
                    err := pc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
                    if err != nil {
                        fmt.Println("Leave due to error")
                        fmt.Println("Error", err)
                        pc.room.leaveChannel <- pc
                        pc.ws.Close()
                    }
                }
                delete(freeRooms, r.name)
            }
        case c:= <- r.questionChannel:
            var questions [roomCapacity]Question
            append(questions, c)
            fmt.Println("Question received ", c)
            if len(questions) == roomCapacity - 1 {
                // Send all questions over all players
                fmt.Println("Notify all players")
                for q := range questions {
                    for pc, _ := range room.playerConns {
                        if pc.player.name != c.playerName {
                            pc.ws.WriteMessage(websocket.TextMessage, "question")
                            pc.ws.WriteJSON(q)
                        }
                    }
                }
            }
        case c := <-r.leaveChannel:
            fmt.Println("Leave")
            r.updateAllPlayers()
            delete(r.playerConns, c)
            if len(r.playerConns) == 0 {
                goto Exit
            }
        case <-r.updateAll:
            fmt.Println("Update all")
            r.updateAllPlayers()
        }
    }

    Exit:

    // delete room
    delete(allRooms, r.name)
    delete(freeRooms, r.name)
    roomsCount -= 1
    log.Print("Room closed:", r.name)
}

func (pc *playerConn) executeCommand(command string) {
        if command == "question" {
            var question Question
            var err interface{}
            err = pc.ws.ReadJSON(&question)

            if err != nil {
                fmt.Println("Error, websocket will be closed")
                pc.ws.Close()
                break
            }

            // Send questions over all player
            question.playerName = pc.player.name
            pc.room.questionChannel <- question
        }

}

func (pc *playerConn) receiver() {
    for {
        _, command, err := pc.ws.ReadMessage()
        if err != nil {
            break
        }
        fmt.Println("Playeer conn with player name ", pc.player.name,
                    " has executed command", command)
        // execute a command
        // pc.Command(string(command))
        // update all conn
        pc.executeCommand(command)
        pc.room.updateAll <- true
    }
    pc.room.leaveChannel <- pc
    pc.ws.Close()
}

func newRoom() *room {
    name := RandString(16)
    room := &room{
        name:        name,
        playerConns: make(map[*playerConn]bool),
        updateAll:   make(chan bool),
        joinChannel:        make(chan *playerConn),
        leaveChannel:       make(chan *playerConn),
    }

    allRooms[name] = room
    freeRooms[name] = room

    // run room
    go room.run()

    roomsCount += 1
    return room
}

func newPlayer(username string) *Player{
    return &Player{
        name:        username,
    }
}

func newPlayerConn(ws *websocket.Conn, player *Player, room *room) *playerConn {
    pc := &playerConn{ws, player, room, ""}
    go pc.receiver()
    return pc
}

/*
    HTTP handlers
*/

func homeHandler(c http.ResponseWriter, r *http.Request) {
    fmt.Println("Index handler")
    var homeTempl = template.Must(template.ParseFiles("templates/home.html"))
    session, _ := store.Get(r, "dating")
    username := session.Values["username"]

    if username == nil {
        fmt.Println("Redirecting to login")
        http.Redirect(c, r, "/login", 302)
        return
    }

    user_name := username.([]string)[0]
    fmt.Println(user_name)

    data := struct {
        Host       string
        RoomsCount int
        Username interface{}
        rooms map[string]*room
    }{r.Host, 0, user_name, freeRooms}
    homeTempl.Execute(c, data)
}

func loginHandler(c http.ResponseWriter,r *http.Request) {
    fmt.Println("Login handler")
    var loginTempl = template.Must(template.ParseFiles("templates/login.html"))

    if r.Method == "GET" {
        log.Println("Login method GET")
        data := struct {
            Error      string
        }{""}

        loginTempl.Execute(c, data)
    } else {
        session, _ := store.Get(r, "dating")

        r.ParseForm()
        fmt.Println("Login POST")
        fmt.Println("request form", r.Form)
        fmt.Println("body", r.Body)
        username := r.Form["username"]

        fmt.Println("username ", username)
        if username == nil {
            data := struct {
                Host       string
                Error      string
            }{r.Host + "/login", "Please, enter username"}

            loginTempl.Execute(c, data)
            return
        }
        fmt.Println("session ", session)
        session.Values["username"] = username
        session.Save(r, c)
        fmt.Println("Redirecting to home page")
        http.Redirect(c, r, "/", 302)
    }
}

func joinOrCreateRoom(c http.ResponseWriter,r *http.Request) {
    ws, err := websocket.Upgrade(c, r, nil, 1024, 1024)
    fmt.Println("Join room")
    if _, ok := err.(websocket.HandshakeError); ok {
        http.Error(c, "Not a websocket handshake", 400)
        return
    } else if err != nil {
        return
    }

    /*
        TODO: should be extracted to separate function.
    */
    session, _ := store.Get(r, "dating")
    user_data := session.Values["username"]

    var freeRoom *room
    if len(freeRooms) != 0 {
        for _, room := range freeRooms{
            freeRoom = room
            break
        }
    } else {
        fmt.Println("New room!!!")
        freeRoom = newRoom()
    }
    fmt.Println("Username ", user_data.([]string)[0],
                " has connected")
    username := user_data.([]string)[0]
    player := newPlayer(username)
    pConn := newPlayerConn(ws, player, freeRoom)
    // Join Player to room
    freeRoom.joinChannel <- pConn
}

func main() {
    roomCapacity = 2
    http.HandleFunc("/", homeHandler)
    http.HandleFunc("/login", loginHandler)
    http.HandleFunc("/join", joinOrCreateRoom)

    http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, r.URL.Path[1:])
    })

    if err := http.ListenAndServe(ADDR, nil); err != nil {
        log.Fatal("ListenAndServe:", err)
    }
}
