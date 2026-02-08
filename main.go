package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	tutorIDEnv = os.Getenv("TUTOR_ID")
	upgrader   = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type Student struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Frequency string `json:"frequency"`
	Callsign  string `json:"callsign"`
	IsPTTing  bool   `json:"is_ptting"`
}

type ConnState struct {
	StudentID string
	IsTutor   bool
}

type Lesson struct {
	ID                 string              `json:"id"`
	TutorID            string              `json:"tutor_id"`
	Students           map[string]*Student `json:"students"`
	Frequencies        []string            `json:"frequencies"`
	Callsigns          []string            `json:"callsigns"`
	ActiveTransmitters map[string]string   `json:"active_transmitters"` // freq -> studentID
	conns              map[*websocket.Conn]*ConnState
	mu                 sync.Mutex
}

var (
	lessons   = make(map[string]*Lesson)
	lessonsMu sync.Mutex
)

func getOrCreateLesson(id string) *Lesson {
	lessonsMu.Lock()
	defer lessonsMu.Unlock()
	if l, ok := lessons[id]; ok {
		return l
	}
	l := &Lesson{
		ID:                 id,
		Students:           make(map[string]*Student),
		Frequencies:        []string{},
		Callsigns:          []string{},
		ActiveTransmitters: make(map[string]string),
		conns:              make(map[*websocket.Conn]*ConnState),
	}
	lessons[id] = l
	return l
}

func main() {
	if tutorIDEnv == "" {
		tutorIDEnv = "admin" // Default if not set
	}

	http.HandleFunc("/", landingHandler)
	http.HandleFunc("/tutor", tutorHandler)
	http.HandleFunc("/student", studentHandler)
	http.HandleFunc("/ws", wsHandler)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	fmt.Printf("Server starting on :8080 with TUTOR_ID=%s\n", tutorIDEnv)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func landingHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	tmpl.Execute(w, nil)
}

func tutorHandler(w http.ResponseWriter, r *http.Request) {
	tID := r.URL.Query().Get("tutorID")
	lID := r.URL.Query().Get("lessonID")

	if tID != tutorIDEnv {
		http.Redirect(w, r, "/?error=Invalid Tutor ID", http.StatusFound)
		return
	}
	if lID == "" {
		http.Redirect(w, r, "/?error=Missing Lesson ID", http.StatusFound)
		return
	}

	getOrCreateLesson(lID)

	tmpl := template.Must(template.ParseFiles("templates/tutor.html"))
	tmpl.Execute(w, map[string]string{"LessonID": lID, "TutorID": tID})
}

func studentHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	lID := r.URL.Query().Get("lessonID")

	if name == "" || lID == "" {
		http.Redirect(w, r, "/?error=Missing Name or Lesson ID", http.StatusFound)
		return
	}

	tmpl := template.Must(template.ParseFiles("templates/student.html"))
	tmpl.Execute(w, map[string]string{"Name": name, "LessonID": lID})
}

type Message struct {
	Type        string     `json:"type"`
	LessonID    string     `json:"lesson_id,omitempty"`
	StudentID   string     `json:"student_id,omitempty"`
	Name        string     `json:"name,omitempty"`
	Frequency   string     `json:"frequency,omitempty"`
	Frequencies []string   `json:"frequencies,omitempty"`
	Callsign    string     `json:"callsign,omitempty"`
	Callsigns   []string   `json:"callsigns,omitempty"`
	IsPTTing    bool       `json:"is_ptting,omitempty"`
	Students    []*Student `json:"students,omitempty"`
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	var currentStudentID string
	var currentLesson *Lesson

	for {
		msgType, msgData, err := conn.ReadMessage()
		if err != nil {
			if currentLesson != nil {
				currentLesson.mu.Lock()
				delete(currentLesson.conns, conn)
				if currentStudentID != "" {
					s, ok := currentLesson.Students[currentStudentID]
					if ok && s.IsPTTing && s.Frequency != "" {
						if currentLesson.ActiveTransmitters[s.Frequency] == currentStudentID {
							delete(currentLesson.ActiveTransmitters, s.Frequency)
						}
					}
					delete(currentLesson.Students, currentStudentID)
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}
			break
		}

		if msgType == websocket.BinaryMessage {
			if currentLesson != nil && currentStudentID != "" {
				currentLesson.mu.Lock()
				student, ok := currentLesson.Students[currentStudentID]
				if ok && student.IsPTTing && student.Frequency != "" {
					if currentLesson.ActiveTransmitters[student.Frequency] == currentStudentID {
						// Prepend sender ID and frequency info to the binary message
						// This helps the receiver filter audio
						// For simplicity, let's just use a JSON-like header or fixed size header
						// Actually, since we know the frequency of each connection, we can just route it.

						for otherConn, state := range currentLesson.conns {
							if otherConn == conn {
								continue
							}
							if state.IsTutor {
								// Tutors get all audio, but they need to know which student it is from
								// Let's prepend the student ID (fixed 16 bytes?)
								// Or just send another message.
								// Let's just send raw audio for now and let tutor hear "everything" if listening.
								// Requirement: "tutor listen in to the frequency... or student"
								// So tutor needs to know which student/freq the audio is from.

								// We'll prepend StudentID followed by Frequency name, null-terminated.
								header := fmt.Sprintf("%s|%s|", student.ID, student.Frequency)
								headerBytes := []byte(header)
								fullMsg := append(headerBytes, msgData...)
								otherConn.WriteMessage(websocket.BinaryMessage, fullMsg)
							} else {
								otherStudent := currentLesson.Students[state.StudentID]
								if otherStudent != nil && otherStudent.Frequency == student.Frequency {
									// Students only get raw audio from their own frequency
									otherConn.WriteMessage(websocket.BinaryMessage, msgData)
								}
							}
						}
					}
				}
				currentLesson.mu.Unlock()
			}
			continue
		}

		var msg Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "join":
			l := getOrCreateLesson(msg.LessonID)
			currentLesson = l
			l.mu.Lock()
			state := &ConnState{IsTutor: msg.StudentID == ""}
			if msg.StudentID != "" {
				currentStudentID = msg.StudentID
				state.StudentID = currentStudentID
				l.Students[currentStudentID] = &Student{
					ID:   msg.StudentID,
					Name: msg.Name,
				}
			}
			l.conns[conn] = state
			l.mu.Unlock()
			broadcastUpdate(l)

		case "add_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				currentLesson.Frequencies = append(currentLesson.Frequencies, msg.Frequency)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[msg.StudentID]; ok {
					if s.IsPTTing && s.Frequency != "" && currentLesson.ActiveTransmitters[s.Frequency] == s.ID {
						delete(currentLesson.ActiveTransmitters, s.Frequency)
					}
					s.Frequency = msg.Frequency
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clear_frequencies":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "add_callsign":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				currentLesson.Callsigns = append(currentLesson.Callsigns, msg.Callsign)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_callsign":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[msg.StudentID]; ok {
					s.Callsign = msg.Callsign
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "ptt":
			if currentLesson != nil && currentStudentID != "" {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[currentStudentID]; ok {
					if msg.IsPTTing {
						if s.Frequency != "" {
							if _, busy := currentLesson.ActiveTransmitters[s.Frequency]; !busy {
								currentLesson.ActiveTransmitters[s.Frequency] = currentStudentID
								s.IsPTTing = true
							} else {
								s.IsPTTing = false
							}
						}
					} else {
						if s.IsPTTing && s.Frequency != "" && currentLesson.ActiveTransmitters[s.Frequency] == currentStudentID {
							delete(currentLesson.ActiveTransmitters, s.Frequency)
						}
						s.IsPTTing = false
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}
		}
	}
}

func broadcastUpdate(l *Lesson) {
	l.mu.Lock()
	defer l.mu.Unlock()

	students := make([]*Student, 0, len(l.Students))
	for _, s := range l.Students {
		students = append(students, s)
	}

	msg := Message{
		Type:        "update",
		LessonID:    l.ID,
		Frequencies: l.Frequencies,
		Callsigns:   l.Callsigns,
		Students:    students,
	}

	data, _ := json.Marshal(msg)

	for conn := range l.conns {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			continue
		}
	}
}
