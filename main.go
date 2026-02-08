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
	IsPTTing  bool   `json:"is_ptting"`
}

type Lesson struct {
	ID          string              `json:"id"`
	TutorID     string              `json:"tutor_id"`
	Students    map[string]*Student `json:"students"`
	Frequencies []string            `json:"frequencies"`
	conns       map[*websocket.Conn]bool
	mu          sync.Mutex
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
		ID:          id,
		Students:    make(map[string]*Student),
		Frequencies: []string{},
		conns:       make(map[*websocket.Conn]bool),
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
	Type      string   `json:"type"`
	LessonID  string   `json:"lesson_id,omitempty"`
	StudentID string   `json:"student_id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Frequency string   `json:"frequency,omitempty"`
	Frequencies []string `json:"frequencies,omitempty"`
	IsPTTing  bool     `json:"is_ptting,omitempty"`
	Students  []*Student `json:"students,omitempty"`
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
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			if currentLesson != nil {
				currentLesson.mu.Lock()
				delete(currentLesson.conns, conn)
				if currentStudentID != "" {
					delete(currentLesson.Students, currentStudentID)
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}
			break
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
			l.conns[conn] = true
			if msg.StudentID != "" {
				currentStudentID = msg.StudentID
				l.Students[currentStudentID] = &Student{
					ID:   msg.StudentID,
					Name: msg.Name,
				}
			}
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
					s.Frequency = msg.Frequency
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clear_frequencies":
			if currentLesson != nil {
				currentLesson.mu.Lock()
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
					s.IsPTTing = msg.IsPTTing
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
		Students:    students,
	}

	data, _ := json.Marshal(msg)

	for conn := range l.conns {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			// Connection likely closed, will be handled in its own read loop
			continue
		}
	}
}
