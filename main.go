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

type LessonType string

const (
	LessonFixed          LessonType = "fixed"           // Students change nothing
	LessonRestrictedFreq LessonType = "restricted-freq" // Students choose from set freqs, can change callsign
	LessonOpen           LessonType = "open"            // Students can create/join any freq
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

type CallsignRequest struct {
	StudentID   string `json:"student_id"`
	StudentName string `json:"student_name"`
	NewCallsign string `json:"new_callsign"`
}

type Lesson struct {
	ID                 string              `json:"id"`
	TutorID            string              `json:"tutor_id"`
	Students           map[string]*Student `json:"students"`
	Frequencies        []string            `json:"frequencies"`
	Callsigns          []string            `json:"callsigns"`
	ActiveTransmitters map[string]string   `json:"active_transmitters"` // freq -> studentID
	conns              map[*websocket.Conn]*ConnState
	IsActive           bool                `json:"is_active"`
	Type               LessonType          `json:"type"`
	CallsignVerify     bool                `json:"callsign_verify"`
	PendingCallsigns   []CallsignRequest   `json:"pending_callsigns"`
	mu                 sync.Mutex
}

var (
	lessons   = make(map[string]*Lesson)
	lessonsMu sync.Mutex
)

func getLesson(id string) *Lesson {
	lessonsMu.Lock()
	defer lessonsMu.Unlock()
	l, ok := lessons[id]
	if !ok || !l.IsActive {
		return nil
	}
	return l
}

func createLesson(id string, tutorID string) *Lesson {
	lessonsMu.Lock()
	defer lessonsMu.Unlock()
	l, ok := lessons[id]
	if !ok {
		l = &Lesson{
			ID:                 id,
			Students:           make(map[string]*Student),
			Frequencies:        []string{},
			Callsigns:          []string{},
			ActiveTransmitters: make(map[string]string),
			conns:              make(map[*websocket.Conn]*ConnState),
			Type:               LessonFixed,
		}
		lessons[id] = l
	}
	l.TutorID = tutorID
	l.IsActive = true
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

	createLesson(lID, tID)

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

	l := getLesson(lID)
	if l == nil {
		http.Redirect(w, r, "/?error=Lesson not found or not active. Tutor must start the lesson first.", http.StatusFound)
		return
	}

	tmpl := template.Must(template.ParseFiles("templates/student.html"))
	tmpl.Execute(w, map[string]string{"Name": name, "LessonID": lID})
}

type Message struct {
	Type             string            `json:"type"`
	LessonID         string            `json:"lesson_id,omitempty"`
	StudentID        string            `json:"student_id,omitempty"`
	Name             string            `json:"name,omitempty"`
	Frequency        string            `json:"frequency,omitempty"`
	Frequencies      []string          `json:"frequencies,omitempty"`
	Callsign         string            `json:"callsign,omitempty"`
	Callsigns        []string          `json:"callsigns,omitempty"`
	IsPTTing         bool              `json:"is_ptting,omitempty"`
	Students         []*Student        `json:"students,omitempty"`
	LessonType       LessonType        `json:"lesson_type,omitempty"`
	CallsignVerify   bool              `json:"callsign_verify,omitempty"`
	PendingCallsigns []CallsignRequest `json:"pending_callsigns,omitempty"`
	Message          string            `json:"message,omitempty"`
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
	var isTutor bool

	for {
		msgType, msgData, err := conn.ReadMessage()
		if err != nil {
			if currentLesson != nil {
				currentLesson.mu.Lock()
				delete(currentLesson.conns, conn)
				if isTutor {
					currentLesson.IsActive = false
					forceLeaveAllStudents(currentLesson)
				}
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
				// Requirement: make sure a student can't transmit or receive if they do not have a freq or call sign
				if ok && student.IsPTTing && student.Frequency != "" && student.Callsign != "" {
					if currentLesson.ActiveTransmitters[student.Frequency] == currentStudentID {
						for otherConn, state := range currentLesson.conns {
							if otherConn == conn {
								continue
							}
							if state.IsTutor {
								header := fmt.Sprintf("%s|%s|", student.ID, student.Frequency)
								headerBytes := []byte(header)
								fullMsg := append(headerBytes, msgData...)
								otherConn.WriteMessage(websocket.BinaryMessage, fullMsg)
							} else {
								otherStudent := currentLesson.Students[state.StudentID]
								if otherStudent != nil && otherStudent.Frequency == student.Frequency && otherStudent.Callsign != "" {
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
			l := getLesson(msg.LessonID)
			if msg.StudentID == "" { // Tutor
				isTutor = true
				lessonsMu.Lock()
				l = lessons[msg.LessonID]
				if l == nil {
					l = &Lesson{
						ID:                 msg.LessonID,
						Students:           make(map[string]*Student),
						Frequencies:        []string{},
						Callsigns:          []string{},
						ActiveTransmitters: make(map[string]string),
						conns:              make(map[*websocket.Conn]*ConnState),
						IsActive:           true,
						Type:               LessonFixed,
					}
					lessons[msg.LessonID] = l
				} else {
					l.IsActive = true
				}
				lessonsMu.Unlock()
			}

			if l == nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"Lesson not found"}`))
				return
			}
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

		case "update_settings":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.Type = msg.LessonType
				currentLesson.CallsignVerify = msg.CallsignVerify
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "add_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				// Students can only add if LessonOpen
				if isTutor || currentLesson.Type == LessonOpen {
					currentLesson.Frequencies = append(currentLesson.Frequencies, msg.Frequency)
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				canAssign := isTutor
				if !isTutor {
					if currentLesson.Type == LessonOpen || currentLesson.Type == LessonRestrictedFreq {
						canAssign = true
						msg.StudentID = currentStudentID
					}
				}
				if canAssign {
					if s, ok := currentLesson.Students[msg.StudentID]; ok {
						if s.IsPTTing && s.Frequency != "" && currentLesson.ActiveTransmitters[s.Frequency] == s.ID {
							delete(currentLesson.ActiveTransmitters, s.Frequency)
						}
						s.Frequency = msg.Frequency
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "remove_frequency":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[msg.StudentID]; ok {
					if s.IsPTTing && s.Frequency != "" && currentLesson.ActiveTransmitters[s.Frequency] == s.ID {
						delete(currentLesson.ActiveTransmitters, s.Frequency)
					}
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "add_callsign":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.Callsigns = append(currentLesson.Callsigns, msg.Callsign)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_callsign":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				canAssign := isTutor
				if !isTutor {
					if currentLesson.Type == LessonOpen || currentLesson.Type == LessonRestrictedFreq {
						// Only if verified or verification disabled
						if !currentLesson.CallsignVerify {
							canAssign = true
							msg.StudentID = currentStudentID
						} else {
							// Add to pending
							currentLesson.PendingCallsigns = append(currentLesson.PendingCallsigns, CallsignRequest{
								StudentID:   currentStudentID,
								StudentName: currentLesson.Students[currentStudentID].Name,
								NewCallsign: msg.Callsign,
							})
						}
					}
				}

				if canAssign {
					// Check for uniqueness
					unique := true
					for _, s := range currentLesson.Students {
						if s.Callsign == msg.Callsign && s.ID != msg.StudentID {
							unique = false
							break
						}
					}
					if unique {
						if s, ok := currentLesson.Students[msg.StudentID]; ok {
							s.Callsign = msg.Callsign
						}
					} else if !isTutor {
						conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"Callsign already in use"}`))
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "approve_callsign":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				// Find and remove from pending
				var req CallsignRequest
				found := false
				for i, r := range currentLesson.PendingCallsigns {
					if r.StudentID == msg.StudentID && r.NewCallsign == msg.Callsign {
						req = r
						currentLesson.PendingCallsigns = append(currentLesson.PendingCallsigns[:i], currentLesson.PendingCallsigns[i+1:]...)
						found = true
						break
					}
				}
				if found {
					// Check uniqueness
					unique := true
					for _, s := range currentLesson.Students {
						if s.Callsign == req.NewCallsign {
							unique = false
							break
						}
					}
					if unique {
						if s, ok := currentLesson.Students[req.StudentID]; ok {
							s.Callsign = req.NewCallsign
						}
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "deny_callsign":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				for i, r := range currentLesson.PendingCallsigns {
					if r.StudentID == msg.StudentID && r.NewCallsign == msg.Callsign {
						currentLesson.PendingCallsigns = append(currentLesson.PendingCallsigns[:i], currentLesson.PendingCallsigns[i+1:]...)
						break
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "remove_callsign":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[msg.StudentID]; ok {
					s.Callsign = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clear_frequencies":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "ptt":
			if currentLesson != nil && currentStudentID != "" {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[currentStudentID]; ok {
					// Requirement: must have both freq and callsign to transmit
					if s.Frequency != "" && s.Callsign != "" {
						if msg.IsPTTing {
							if _, busy := currentLesson.ActiveTransmitters[s.Frequency]; !busy {
								currentLesson.ActiveTransmitters[s.Frequency] = currentStudentID
								s.IsPTTing = true
							} else {
								s.IsPTTing = false
							}
						} else {
							if s.IsPTTing && currentLesson.ActiveTransmitters[s.Frequency] == currentStudentID {
								delete(currentLesson.ActiveTransmitters, s.Frequency)
							}
							s.IsPTTing = false
						}
					} else {
						s.IsPTTing = false
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clean_students":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				forceLeaveAllStudents(currentLesson)
				currentLesson.Students = make(map[string]*Student)
				currentLesson.ActiveTransmitters = make(map[string]string)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clean_frequencies":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.Frequencies = []string{}
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clean_callsigns":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.Callsigns = []string{}
				for _, s := range currentLesson.Students {
					s.Callsign = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "end_ex":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "end_lesson":
			if currentLesson != nil && isTutor {
				currentLesson.mu.Lock()
				currentLesson.IsActive = false
				forceLeaveAllStudents(currentLesson)
				currentLesson.mu.Unlock()
			}
		}
	}
}

func forceLeaveAllStudents(l *Lesson) {
	msg := map[string]string{"type": "force_leave"}
	data, _ := json.Marshal(msg)
	for conn, state := range l.conns {
		if !state.IsTutor {
			conn.WriteMessage(websocket.TextMessage, data)
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
		Type:             "update",
		LessonID:         l.ID,
		Frequencies:      l.Frequencies,
		Callsigns:        l.Callsigns,
		Students:         students,
		LessonType:       l.Type,
		CallsignVerify:   l.CallsignVerify,
		PendingCallsigns: l.PendingCallsigns,
	}

	data, _ := json.Marshal(msg)

	for conn := range l.conns {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			continue
		}
	}
}
