package main

import (
    "html/template"
    "log"
    "net/http"
    "crypto/rand"
    "io"
    "io/ioutil"
    "os"
    "sync"
    "github.com/gorilla/sessions"
    "github.com/gorilla/securecookie"
    "github.com/gorilla/websocket"
    "database/sql"
)

const (
    ADDR string = ":9000"
)

var store = sessions.NewCookieStore([]byte(
                                    securecookie.GenerateRandomKey(256)))
var freeRooms = make(map[string]*Room)
var allRooms = make(map[string]*Room)
var roomsCount int
var roomCapacity int

// Initializing logging objects
var (
    Trace   *log.Logger
    Info    *log.Logger
    Warning *log.Logger
    Error   *log.Logger
)

type appContext struct {
  db *sql.DB
}

func Init(
    traceHandle io.Writer,
    infoHandle io.Writer,
    warningHandle io.Writer,
    errorHandle io.Writer) {

    Trace = log.New(traceHandle,
        "TRACE: ",
        log.Ldate|log.Ltime|log.Lshortfile)

    Info = log.New(infoHandle,
        "INFO: ",
        log.Ldate|log.Ltime|log.Lshortfile)

    Warning = log.New(warningHandle,
        "WARNING: ",
        log.Ldate|log.Ltime|log.Lshortfile)

    Error = log.New(errorHandle,
        "ERROR: ",
        log.Ldate|log.Ltime|log.Lshortfile)
}


/*
    Structures used
*/

type Room struct {
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

    answerChannel chan []Question

    answersMap map[string][]Question

    questionMap map[string]Question

    likesMap map[string]string

    likeChannel chan Like

    men int

    women int

    manLock sync.RWMutex

    womanLock sync.RWMutex
}

type Player struct {
    name  string
    sex bool
}

type playerConn struct {
    ws *websocket.Conn
    player *Player
    room *Room
    question string
}

type Message struct{
    message string
}

type Like struct {
    Sender string
    Receiver string
}

type Command struct {
    Cmd string
    Payload interface{}
    Sender string
}

type Question struct {
    Question string
    Answer string
    PlayerName string
    Respondent string
    respondentSex bool
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
        Info.Println("Send message")
        err := pc.ws.WriteMessage(websocket.TextMessage, []byte(msg))
        if err != nil {
            Info.Println("Leave due to error")
            pc.room.leaveChannel <- pc
            pc.ws.Close()
        }
    }()
}

func (r *Room) updateAllPlayers() {
    for c := range r.playerConns {
        c.sendState()
    }
}

// Run the room in goroutine
func (r *Room) run() {
    Info.Println("Room is running")
    // make buffer fo questions

    for {
        select {
            case c :=<-r.joinChannel:
                r.playerConns[c] = true
                // r.updateAllPlayers()
                // if room is full - delete from freeRooms
                Info.Println("Join channel")
                Info.Println(len(r.playerConns))
                Info.Println(roomCapacity)

                if len(r.playerConns) == roomCapacity {
                    Info.Println("Room ", r.name, " is full")
                    for pc, _ := range r.playerConns {
                        Info.Println("Send acknowledgement to player ", pc.player.name)

                        msg := "full"
                        cmd := &Command{
                            Cmd: msg,
                            Payload: "",
                        }

                        err := pc.ws.WriteJSON(cmd)
                        if err != nil {
                            Error.Println(err)
                            pc.room.leaveChannel <- pc
                            pc.ws.Close()
                        }
                    }
                    Info.Println("Delete room ", r.name," from free rooms")
                    delete(freeRooms, r.name)
                }
            case questionReceived := <-r.questionChannel:
                Info.Println("Question received ", questionReceived)
                r.questionMap[questionReceived.PlayerName] = questionReceived
                Info.Println("Question buffer len is ", len(r.questionMap))
                Info.Println(r.questionMap)
                if len(r.questionMap) == roomCapacity {
                    // Send all questions over all players
                    Info.Println("Notify all players")
                    for pc, _ := range r.playerConns {
                        questionsList := make([]Question, 0, roomCapacity - 1)

                        for _, question := range r.questionMap {
                            if pc.player.name != question.PlayerName &&
                                pc.player.sex != question.respondentSex{
                                Info.Println("Append question to list")
                                questionsList = append(questionsList, question)
                            }
                        }
                        Info.Println("Question list to send is ", questionsList)
                        cmd := &Command{
                                    Cmd: "question",
                                    Payload: questionsList,
                                    Sender: "",
                        }
                        Info.Println("Send question map to player ", pc.player.name)
                        pc.ws.WriteJSON(cmd)
                    }
                    // r.questionMap = make(map[string]Question, roomCapacity)
                }
            case answers := <- r.answerChannel:
                Info.Println("Answers received ", answers)
                for _, answer := range answers {

                    player_answers, ok := r.answersMap[answer.PlayerName]

                    if ok {
                        player_answers = append(player_answers, answer)
                    } else {
                        player_answers := make([]Question, 0)
                        player_answers = append(player_answers, answer)
                        r.answersMap[answer.PlayerName] = player_answers
                    }
                }
                Info.Println("Answer map: ",r.answersMap)

                if len(r.answersMap) == roomCapacity {
                        for pc, _ := range r.playerConns{
                            answers = r.answersMap[pc.player.name]
                            cmd := &Command{
                                    Cmd: "results",
                                    Payload: answers,
                                    Sender: "",
                            }
                            Info.Println("Answers ", answers," are sent to player ", pc.player.name)
                            pc.ws.WriteJSON(cmd)
                        }
                }
            case like := <- r.likeChannel:
                Info.Println(like)
                r.likesMap[like.Sender] = like.Receiver

                if len(r.likesMap) == roomCapacity {
                    // TODO: process likes co incidences
                }
            case c := <-r.leaveChannel:
                Info.Println("Leave channel")
                r.updateAllPlayers()
                delete(r.playerConns, c)
                if len(r.playerConns) == 0 {
                    goto Exit
                }
            case <-r.updateAll:
                Info.Println("Update all")
                r.updateAllPlayers()
        }
        Info.Println("Room ", r.name," is waiting for new events")
    }

    Exit:

    // delete room
    delete(allRooms, r.name)
    delete(freeRooms, r.name)
    roomsCount -= 1
    Info.Println("Room closed:", r.name)
}


func executeCommand(pc *playerConn, command string) {
        Info.Println("Command ", command, " has been executed")
        if command == "question" {
            var question Question
            var err interface{}
            err = pc.ws.ReadJSON(&question)

            if err != nil {
                Info.Println("Error, websocket will be closed")
                pc.ws.Close()
                return
            }
            Info.Println("Question was red ", question)
            // Send questions over all player
            question.PlayerName = pc.player.name
            question.respondentSex = pc.player.sex
            Info.Println("Question is sent to room ", pc.room.name," question channel")
            Info.Println("Question channel length : ", len(pc.room.questionChannel))
            pc.room.questionChannel <- question
        }

        if command == "answers" {
            var err interface{}
            var answers = make([]Question, roomCapacity / 2, roomCapacity / 2)
            err = pc.ws.ReadJSON(&answers)

            if err != nil {
                Info.Println("Error, websocket will be closed")
                pc.ws.Close()
                return
            }

            Info.Println("Questions and answers: ", answers)

            pc.room.answerChannel <- answers
        }

        if command == "like" {
            var like Like
            err := pc.ws.ReadJSON(&like)

            if err != nil {
                Info.Println("Error, websocket will be closed")
                pc.ws.Close()
                return
            }

            Info.Println("Like was red ", like)

            // TODO: send like through the like channel
        }
}


func (pc *playerConn) receiver() {
    for {
        _, command, err := pc.ws.ReadMessage()
        if err != nil {
            break
        }
        Info.Println("PC with player name ", pc.player.name,
                    " execute command: ", string(command))
        // execute a command
        // update all conn
        executeCommand(pc, string(command))
        pc.room.updateAll <- true
    }
    pc.room.leaveChannel <- pc
    pc.ws.Close()
}

func newRoom() *Room {
    name := RandString(16)
    room := &Room{
        name:        name,
        playerConns: make(map[*playerConn]bool),
        updateAll:   make(chan bool),
        joinChannel:        make(chan *playerConn),
        leaveChannel:       make(chan *playerConn),
        questionChannel: make(chan Question),
        answerChannel: make(chan []Question),
        answersMap: make(map[string][]Question, roomCapacity),
        questionMap: make(map[string]Question, roomCapacity * 2),
        likesMap: make(map[string]string, roomCapacity),
        leaveChannel: make(chan Like),
        men: roomCapacity / 2,
        women: roomCapacity / 2,
    }

    allRooms[name] = room
    freeRooms[name] = room

    // run room
    go room.run()

    roomsCount += 1
    return room
}

func newPlayer(username string, sex bool) *Player{
    return &Player{
        name: username,
        sex: sex,
    }
}

func newPlayerConn(ws *websocket.Conn, player *Player, room *Room) *playerConn {
    pc := &playerConn{ws, player, room, ""}
    go pc.receiver()
    return pc
}


// Function that gets request handler and covers it with authentication.
//func  (c *appContext) authHandler(next http.Handler) http.Handler {
//    fn := func(w http.ResponseWriter, r *http.Request) {
//        authToken := r.Header.Get("Authorization")
//        Info.Println(authToken)
//        user, err := "username", nil //getUser(authToken)
//        //user, err := getUser(c.db, authToken)
//
//        if err != nil {
//            Info.Println("Redirecting to login")
//            http.Redirect(w, r, "/login", 302)
//            return
//        }
//
//        context.Set(r, "user", user)
//        next.ServeHTTP(w, r)
//    }
//
//    return http.HandlerFunc(fn)
//}


/*
    HTTP handlers
*/

func getOrCreateRoom(username string, sex bool) *Room {
    var freeRoom *Room
    freeRoom = nil

    if len(freeRooms) != 0 {
        for _, room := range freeRooms {
            if sex == true {
                if room.men > 0{
                    room.manLock.Lock()

                    if room.men > 0 {
                        freeRoom = room
                        room.men -= 1
                    }
                    room.manLock.Unlock()
                }
            } else {
               if room.women > 0 {
                   room.womanLock.Lock()

                   if room.women > 0 {
                       freeRoom = room
                       room.women -= 1
                   }

                   room.womanLock.Unlock()
               }
            }
        }

        if freeRoom != nil {
            Info.Println("Selected room is ", freeRoom.name)
        }
    }

    if freeRoom == nil {
        freeRoom = newRoom()
        Info.Println("Create new room", freeRoom.name)

        if sex == true {
            Info.Println("Join men rooms")
            freeRoom.men -= 1
        } else {
            Info.Println("Join women room")
            freeRoom.women -= 1
        }
        freeRooms[freeRoom.name] = freeRoom
        Info.Println("Free rooms ", freeRooms)
    }

    return freeRoom
}


func homeHandler(c http.ResponseWriter, r *http.Request) {
    Info.Println("Index handler")
    var homeTempl = template.Must(template.ParseFiles("templates/home.html"))
    session, _ := store.Get(r, "dating")
    username := session.Values["username"]

    if username == nil {
        Info.Println("Redirecting to login")
        http.Redirect(c, r, "/login", 302)
        return
    }

    user_name := username.([]string)[0]
    Info.Println(user_name)

    data := struct {
        Host       string
        RoomsCount int
        Username interface{}
        rooms map[string]*Room
    }{r.Host, 0, user_name, freeRooms}
    homeTempl.Execute(c, data)
}

func loginHandler(c http.ResponseWriter,r *http.Request) {
    Info.Println("Login handler")
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
        Info.Println("Login POST")
        username := r.Form["username"]
        sex := r.Form["sex"]

        Info.Println("username ", username)
        if username == nil {
            data := struct {
                Host       string
                Error      string
            }{r.Host + "/login", "Please, enter username"}

            loginTempl.Execute(c, data)
            return
        }
        Info.Println("session ", session)
        session.Values["username"] = username
        session.Values["sex"] = sex
        session.Save(r, c)
        Info.Println("Redirecting to home page")
        http.Redirect(c, r, "/", 302)
    }
}

func joinOrCreateRoom(c http.ResponseWriter,r *http.Request) {
    ws, err := websocket.Upgrade(c, r, nil, 1024, 1024)
    Info.Println("Join room")
    if _, ok := err.(websocket.HandshakeError); ok {
        http.Error(c, "Not a websocket handshake", 400)
        return
    } else if err != nil {
        return
    }

    session, _ := store.Get(r, "dating")
    user_data := session.Values["username"]
    sex_value := session.Values["sex"]

    var freeRoom *Room
    var sex bool

    username := user_data.([]string)[0]
    Info.Println("sex value ", sex_value)
    sex_description := sex_value.([]string)[0]

    if sex_description == "male" {
        sex = true
    } else {
        sex = false
    }

    freeRoom = getOrCreateRoom(username, sex)
    Info.Println("Username ", username,
                " has connected")
    player := newPlayer(username, sex)
    pConn := newPlayerConn(ws, player, freeRoom)

    // Join Player to room
    freeRoom.joinChannel <- pConn
}

func main() {
    Init(ioutil.Discard, os.Stdout, os.Stdout, os.Stderr)

    roomCapacity = 2
    http.HandleFunc("/", homeHandler)
    http.HandleFunc("/login", loginHandler)
    http.HandleFunc("/join", joinOrCreateRoom)

    Info.Println("Start server on port", ADDR[1:])
    http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, r.URL.Path[1:])
    })

    if err := http.ListenAndServe(ADDR, nil); err != nil {
        log.Fatal("ListenAndServe:", err)
    }
}
