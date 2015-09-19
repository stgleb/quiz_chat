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
var RoomsCount int

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
}

type Player struct {
    name  string
}

type playerConn struct {
    ws *websocket.Conn
    player *Player
    room *room
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
        err := pc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
        if err != nil {
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
    for {
        select {
        case c := <-r.joinChannel:
            r.playerConns[c] = true
            r.updateAllPlayers()

        // if room is full - delete from freeRooms
            if len(r.playerConns) == 2 {
                delete(freeRooms, r.name)
            }

        case c := <-r.leaveChannel:
            r.updateAllPlayers()
            delete(r.playerConns, c)
            if len(r.playerConns) == 0 {
                goto Exit
            }
        case <-r.updateAll:
            r.updateAllPlayers()
        }
    }

    Exit:

    // delete room
    delete(allRooms, r.name)
    delete(freeRooms, r.name)
    RoomsCount -= 1
    log.Print("Room closed:", r.name)
}

func (pc *playerConn) receiver() {
    for {
        _, command, err := pc.ws.ReadMessage()
        if err != nil {
            break
        }
        log.Println("Playeer conn with player name %s has executed command %s",
                    pc.player.name, command)
        // execute a command
        // pc.Command(string(command))
        // update all conn
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

    RoomsCount += 1
    return room
}

func newPlayer(username string) *Player{
    return &Player{
        name:        username,
    }
}

func newPlayerConn(ws *websocket.Conn, player *Player, room *room) *playerConn {
    pc := &playerConn{ws, player, room}
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
    }

    data := struct {
        Host       string
        RoomsCount int
        Username interface{}
        rooms map[string]*room
    }{r.Host, 0, username, freeRooms}
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

func joinOrCreateroom(c http.ResponseWriter,r *http.Request) {
    ws, err := websocket.Upgrade(c, r, nil, 1024, 1024)
    if _, ok := err.(websocket.HandshakeError); ok {
        http.Error(c, "Not a websocket handshake", 400)
        return
    } else if err != nil {
        return
    }

    /*
        TODO: should be extracted to separate function.
    */

    session, err := store.Get(r, "dating")
    username := session.Values["username"]

    if err != nil {
        http.Error(c, err.Error(), 500)
        return
    }

    var freeRoom *room
    if len(freeRooms) != 0 {
        for _, room := range freeRooms{
            freeRoom = room
            break
        }
    } else {
        freeRoom = newRoom()
    }

    player := newPlayer(username.(string))
    pConn := newPlayerConn(ws, player, freeRoom)
    // Join Player to room
    freeRoom.joinChannel <- pConn
}

func main() {
    fmt.Printf("Hello")
    http.HandleFunc("/", homeHandler)
    http.HandleFunc("/login", loginHandler)
    http.HandleFunc("/rooms", joinOrCreateroom)

    http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, r.URL.Path[1:])
    })

    if err := http.ListenAndServe(ADDR, nil); err != nil {
        log.Fatal("ListenAndServe:", err)
    }
}
