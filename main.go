package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"strings"
	"time"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	systemCodeEnv = os.Getenv("SYSTEM_CODE")
	upgrader      = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type LessonType string

const (
	LessonFixed          LessonType = "fixed"
	LessonRestrictedFreq LessonType = "restricted-freq"
	LessonOpen           LessonType = "open"
)

type BlockType string

const (
	BlockNone BlockType = ""
	BlockTemp BlockType = "temporary"
	BlockPerm BlockType = "permanent"
)

type IPStats struct {
	Attempts       int         `json:"attempts"`
	RecentFailures []time.Time `json:"-"`
	BlockStatus    BlockType   `json:"block_status"`
	LastAttempt    time.Time   `json:"last_attempt"`
}

type SecurityManager struct {
	Stats map[string]*IPStats
	mu    sync.Mutex
}

var sm = &SecurityManager{
	Stats: make(map[string]*IPStats),
}

func (s *SecurityManager) RecordAttempt(ip string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats, ok := s.Stats[ip]
	if !ok {
		stats = &IPStats{}
		s.Stats[ip] = stats
	}

	stats.LastAttempt = time.Now()
	if success {
		return
	}

	stats.Attempts++
	stats.RecentFailures = append(stats.RecentFailures, time.Now())

	now := time.Now()
	var recent []time.Time
	for _, t := range stats.RecentFailures {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	stats.RecentFailures = recent

	if stats.BlockStatus == BlockPerm {
		return
	}

	if stats.Attempts >= 20 {
		stats.BlockStatus = BlockPerm
	} else if len(stats.RecentFailures) >= 5 {
		stats.BlockStatus = BlockTemp
	}
}

func (s *SecurityManager) IsBlocked(ip string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats, ok := s.Stats[ip]
	if !ok {
		return false, ""
	}

	if stats.BlockStatus == BlockPerm {
		return true, "Your IP has been permanently blocked due to repeated failed attempts."
	}

	if stats.BlockStatus == BlockTemp {
		if time.Since(stats.LastAttempt) < time.Minute {
			return true, "Your IP is temporarily blocked. Please try again in a minute."
		}
		stats.BlockStatus = BlockNone
	}

	return false, ""
}

func getIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func isAdmin(r *http.Request) bool {
	cookie, err := r.Cookie("admin_access")
	if err != nil {
		return false
	}
	return cookie.Value == systemCodeEnv
}

type Student struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Frequency      string `json:"frequency"`
	Callsign       string `json:"callsign"`
	IsPTTing       bool   `json:"is_ptting"`
	HasPermissions bool   `json:"has_permissions"`
}

type LogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Type       string    `json:"type"` // "event", "transmission"
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	Callsign   string    `json:"callsign"`
	Frequency  string    `json:"frequency"`
	Details    string    `json:"details,omitempty"`
	AudioFile  string    `json:"audio_file,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
}

type ConnState struct {
	UserID      string
	IsTutor     bool
	IsMainTutor bool
}

type CallsignRequest struct {
	StudentID   string `json:"student_id"`
	StudentName string `json:"student_name"`
	NewCallsign string `json:"new_callsign"`
}

type TutorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Lesson struct {
	ID                 string              `json:"id"`
	MainTutorID        string              `json:"main_tutor_id"`
	Tutors             map[string]string   `json:"tutors"` // ID -> Name
	Students           map[string]*Student `json:"students"`
	Frequencies        []string            `json:"frequencies"`
	Callsigns          []string            `json:"callsigns"`
	ActiveTransmitters map[string]string   `json:"active_transmitters"`
	ActiveRecordings   map[string][]byte    `json:"-"` // UserID -> audio data
	CurrentTranscripts map[string]string    `json:"-"` // UserID -> transcript
	conns              map[*websocket.Conn]*ConnState
	IsActive           bool                `json:"is_active"`
	Type               LessonType          `json:"type"`
	CallsignVerify     bool                `json:"callsign_verify"`
	PendingCallsigns   []CallsignRequest   `json:"pending_callsigns"`
	Logs               []LogEntry          `json:"logs"`
	StartTime          time.Time           `json:"start_time"`
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

func createLesson(id string, tutorID string, tutorName string) *Lesson {
	lessonsMu.Lock()
	defer lessonsMu.Unlock()
	l, ok := lessons[id]
	if !ok {
		l = &Lesson{
			ID:                 id,
			MainTutorID:        tutorID,
			Tutors:             make(map[string]string),
			Students:           make(map[string]*Student),
			Frequencies:        []string{},
			Callsigns:          []string{},
			ActiveTransmitters: make(map[string]string),
		ActiveRecordings:   make(map[string][]byte),
		CurrentTranscripts: make(map[string]string),
			conns:              make(map[*websocket.Conn]*ConnState),
			Type:               LessonFixed,
			StartTime:          time.Now(),
		}
		lessons[id] = l
		l.logEvent(tutorID, tutorName, "TUTOR", "", "Lesson created")
	}
	if l.MainTutorID == "" {
		l.MainTutorID = tutorID
	}
	l.Tutors[tutorID] = tutorName
	l.IsActive = true
	return l
}

func main() {
	if systemCodeEnv == "" {
		systemCodeEnv = "admin"
	}
	os.MkdirAll("logs", 0755)

	http.HandleFunc("/", landingHandler)
	http.HandleFunc("/tutor", tutorHandler)
	http.HandleFunc("/student", studentHandler)
	http.HandleFunc("/admin", adminHandler)
	http.HandleFunc("/admin/unblock", unblockHandler)
	http.HandleFunc("/admin/block", manualBlockHandler)
	http.HandleFunc("/admin/toggle-perm", togglePermHandler)
	http.HandleFunc("/admin/download", downloadZipHandler)
	http.HandleFunc("/run-tests", runTestsHandler)
	http.HandleFunc("/check_name", checkNameHandler)
	http.HandleFunc("/ws", wsHandler)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	fmt.Printf("Server starting on :8080 with SYSTEM_CODE set\n")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func landingHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	tmpl.Execute(w, nil)
}

func tutorHandler(w http.ResponseWriter, r *http.Request) {
	ip := getIP(r)
	if blocked, msg := sm.IsBlocked(ip); blocked {
		http.Redirect(w, r, "/?error="+template.URLQueryEscaper(msg), http.StatusFound)
		return
	}

	tID := r.URL.Query().Get("systemCode")
	tName := r.URL.Query().Get("tutorName")
	lID := r.URL.Query().Get("lessonID")
	// If no tutorID provided, we generate one or use a consistent one?
	// The prompt says "first tutor is the main tutor".
	// Let's use a unique ID for each tutor session.
	uID := r.URL.Query().Get("uID")
	if uID == "" {
		uID = fmt.Sprintf("tutor_%d", time.Now().UnixNano())
	}

	if tID != systemCodeEnv {
		sm.RecordAttempt(ip, false)
		http.Redirect(w, r, "/?error=Invalid System Code", http.StatusFound)
		return
	}
	if lID == "" || tName == "" {
		http.Redirect(w, r, "/?error=Missing Lesson ID or Tutor Name", http.StatusFound)
		return
	}

	sm.RecordAttempt(ip, true)

	http.SetCookie(w, &http.Cookie{
		Name:  "admin_access",
		Value: tID,
		Path:  "/",
	})

	createLesson(lID, uID, tName)

	tmpl := template.Must(template.ParseFiles("templates/tutor.html"))
	tmpl.Execute(w, map[string]string{"LessonID": lID, "TutorName": tName, "SystemCode": tID, "UserID": uID})
}

func studentHandler(w http.ResponseWriter, r *http.Request) {
	ip := getIP(r)
	if blocked, msg := sm.IsBlocked(ip); blocked {
		http.Redirect(w, r, "/?error="+template.URLQueryEscaper(msg), http.StatusFound)
		return
	}

	name := r.URL.Query().Get("name")
	lID := r.URL.Query().Get("lessonID")
	uID := r.URL.Query().Get("uID")
	if uID == "" {
		uID = fmt.Sprintf("student_%d", time.Now().UnixNano())
	}

	if name == "" || lID == "" {
		http.Redirect(w, r, "/?error=Missing Name or Lesson ID", http.StatusFound)
		return
	}

	l := getLesson(lID)
	if l == nil {
		sm.RecordAttempt(ip, false)
		http.Redirect(w, r, "/?error=Lesson not found or not active. Tutor must start the lesson first.", http.StatusFound)
		return
	}

	sm.RecordAttempt(ip, true)

	// Find the main tutor's name
	mainTutorName := l.Tutors[l.MainTutorID]

	tmpl := template.Must(template.ParseFiles("templates/student.html"))
	tmpl.Execute(w, map[string]string{"Name": name, "LessonID": lID, "TutorName": mainTutorName, "UserID": uID})
}

func checkNameHandler(w http.ResponseWriter, r *http.Request) {
	lessonID := r.URL.Query().Get("lesson_id")
	name := r.URL.Query().Get("name")

	lessonsMu.Lock()
	l, ok := lessons[lessonID]
	lessonsMu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.Students {
		if s.Name == name {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("taken"))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func downloadZipHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	file := r.URL.Query().Get("file")
	if file == "" || !strings.HasPrefix(file, "logs/") || strings.Contains(file, "..") {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+strings.TrimPrefix(file, "logs/"))
	http.ServeFile(w, r, file)
}

func adminHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Redirect(w, r, "/?error=Unauthorized Admin Access", http.StatusFound)
		return
	}

	lessonsMu.Lock()
	activeLessons := []map[string]interface{}{}
	for _, l := range lessons {
		if l.IsActive {
			mainTutorName := l.Tutors[l.MainTutorID]
			activeLessons = append(activeLessons, map[string]interface{}{
				"ID":           l.ID,
				"TutorName":    mainTutorName,
				"StudentCount": len(l.Students),
				"Type":         l.Type,
			})
		}
	}
	lessonsMu.Unlock()

	sm.mu.Lock()
	blockedIPs := []map[string]interface{}{}
	for ip, stats := range sm.Stats {
		if stats.BlockStatus != BlockNone || stats.Attempts > 0 {
			blockedIPs = append(blockedIPs, map[string]interface{}{
				"IP":          ip,
				"Attempts":    stats.Attempts,
				"BlockStatus": stats.BlockStatus,
				"LastAttempt": stats.LastAttempt.Format(time.RFC3339),
			})
		}
	}
	sm.mu.Unlock()

	// List zip files in logs/
	zips := []string{}
	files, _ := os.ReadDir("logs")
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".zip") {
			zips = append(zips, "logs/"+f.Name())
		}
	}

	data := map[string]interface{}{
		"Lessons":    activeLessons,
		"BlockedIPs": blockedIPs,
		"Zips":       zips,
	}

	tmpl := template.Must(template.ParseFiles("templates/admin.html"))
	tmpl.Execute(w, data)
}

func unblockHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ip := r.URL.Query().Get("ip")
	if ip != "" {
		sm.mu.Lock()
		if stats, ok := sm.Stats[ip]; ok {
			stats.BlockStatus = BlockNone
			stats.Attempts = 0
			stats.RecentFailures = nil
		}
		sm.mu.Unlock()
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func manualBlockHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ip := r.URL.Query().Get("ip")
	if ip != "" {
		sm.mu.Lock()
		stats, ok := sm.Stats[ip]
		if !ok {
			stats = &IPStats{}
			sm.Stats[ip] = stats
		}
		stats.BlockStatus = BlockPerm
		sm.mu.Unlock()
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func togglePermHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ip := r.URL.Query().Get("ip")
	if ip != "" {
		sm.mu.Lock()
		if stats, ok := sm.Stats[ip]; ok {
			if stats.BlockStatus == BlockTemp {
				stats.BlockStatus = BlockPerm
			} else if stats.BlockStatus == BlockPerm {
				stats.BlockStatus = BlockTemp
			}
		}
		sm.mu.Unlock()
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func runTestsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	out, _ := exec.Command("go", "test", "-v", ".").CombinedOutput()
	w.Header().Set("Content-Type", "text/plain")
	w.Write(out)
}

type Message struct {
	Type             string            `json:"type"`
	LessonID         string            `json:"lesson_id,omitempty"`
	StudentID        string            `json:"student_id,omitempty"`
	UserID           string            `json:"user_id,omitempty"`
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
	TutorName        string            `json:"tutor_name,omitempty"`
	Tutors           []TutorInfo       `json:"tutors,omitempty"`
	MainTutorID      string            `json:"main_tutor_id,omitempty"`
	BroadcastType    string            `json:"broadcast_type,omitempty"` // "freq" or "global"
	TargetID         string            `json:"target_id,omitempty"`
	NewRole          string            `json:"new_role,omitempty"`
	HasPermissions   bool              `json:"has_permissions,omitempty"`
	Text             string            `json:"text,omitempty"`
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	var currentUserID string
	var currentLesson *Lesson
	var state *ConnState

	for {
		msgType, msgData, err := conn.ReadMessage()
		if err != nil {
			if currentLesson != nil {
				currentLesson.mu.Lock()
				delete(currentLesson.conns, conn)
				if state != nil && state.IsTutor {
					// We only end lesson if the MAIN tutor disconnects?
					// "if the tutor end's their session, it ends all student sessions"
					// Maybe it should be if the Main Tutor ends it.
					if state.IsMainTutor {
						currentLesson.IsActive = false
						forceLeaveAllStudents(currentLesson)
					}
					delete(currentLesson.Tutors, state.UserID)
				}
				if currentUserID != "" {
					s, ok := currentLesson.Students[currentUserID]
					if ok && s.IsPTTing && s.Frequency != "" {
						if currentLesson.ActiveTransmitters[s.Frequency] == currentUserID {
							delete(currentLesson.ActiveTransmitters, s.Frequency)
						}
					}
					delete(currentLesson.Students, currentUserID)
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}
			break
		}

		if msgType == websocket.BinaryMessage {
			if currentLesson != nil && currentUserID != "" && state != nil {
				currentLesson.mu.Lock()
				if state.IsTutor {
					// Tutor binary message: "TUTOR|Target|" + audio
					// Target is freq name or "GLOBAL"
					data := msgData
					if len(data) > 6 && string(data[:6]) == "TUTOR|" {
						headerEnd := -1
						pipes := 0
						for i := 0; i < len(data); i++ {
							if data[i] == '|' {
								pipes++
								if pipes == 2 {
									headerEnd = i
									break
								}
							}
						}
						if headerEnd != -1 {
							headerParts := strings.Split(string(data[:headerEnd]), "|")
							target := headerParts[1]
							audio := data[headerEnd+1:]

							currentLesson.ActiveRecordings[currentUserID] = append(currentLesson.ActiveRecordings[currentUserID], audio...)

							for otherConn, otherState := range currentLesson.conns {
								if otherConn == conn {
									continue
								}
								if otherState.IsTutor {
									// Tutors hear everything from other tutors too?
									// Usually helpful. Let's send it.
									otherConn.WriteMessage(websocket.BinaryMessage, msgData)
								} else {
									s := currentLesson.Students[otherState.UserID]
									if s != nil && s.Callsign != "" {
										if target == "GLOBAL" || s.Frequency == target {
											otherConn.WriteMessage(websocket.BinaryMessage, audio)
										}
									}
								}
							}
						}
					}
				} else {
					student, ok := currentLesson.Students[currentUserID]
					if ok && student.IsPTTing && student.Frequency != "" && student.Callsign != "" {
						if currentLesson.ActiveTransmitters[student.Frequency] == currentUserID {
							currentLesson.ActiveRecordings[currentUserID] = append(currentLesson.ActiveRecordings[currentUserID], msgData...)
							for otherConn, otherState := range currentLesson.conns {
								if otherConn == conn {
									continue
								}
								if otherState.IsTutor {
									header := fmt.Sprintf("%s|%s|", student.ID, student.Frequency)
									headerBytes := []byte(header)
									fullMsg := append(headerBytes, msgData...)
									otherConn.WriteMessage(websocket.BinaryMessage, fullMsg)
								} else {
									otherStudent := currentLesson.Students[otherState.UserID]
									if otherStudent != nil && otherStudent.Frequency == student.Frequency && otherStudent.Callsign != "" {
										otherConn.WriteMessage(websocket.BinaryMessage, msgData)
									}
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
			isTutor := msg.StudentID == "" && msg.UserID != ""
			if isTutor {
				lessonsMu.Lock()
				l = lessons[msg.LessonID]
				if l != nil {
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
			currentUserID = msg.UserID
			if isTutor {
				l.Tutors[currentUserID] = msg.Name
				l.logEvent(currentUserID, msg.Name, "TUTOR", "", "Tutor joined")
			}
			state = &ConnState{
				UserID:      currentUserID,
				IsTutor:     isTutor,
				IsMainTutor: isTutor && l.MainTutorID == currentUserID,
			}
			if !isTutor {
				l.Students[currentUserID] = &Student{
					ID:   currentUserID,
					Name: msg.Name,
				}
				l.logEvent(currentUserID, msg.Name, "", "", "Student joined")
			}
			l.conns[conn] = state
			l.mu.Unlock()
			broadcastUpdate(l)

		case "update_settings":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				currentLesson.Type = msg.LessonType
				currentLesson.CallsignVerify = msg.CallsignVerify
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "add_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				if (state != nil && state.IsTutor) || currentLesson.Type == LessonOpen {
					currentLesson.Frequencies = append(currentLesson.Frequencies, msg.Frequency)
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_frequency":
			if currentLesson != nil {
				currentLesson.mu.Lock()
				canAssign := state != nil && state.IsTutor
				targetID := msg.StudentID
				if !canAssign && state != nil {
					if currentLesson.Type == LessonOpen || currentLesson.Type == LessonRestrictedFreq {
						canAssign = true
						targetID = state.UserID
					}
				}
				if canAssign {
					if s, ok := currentLesson.Students[targetID]; ok {
						if s.IsPTTing && s.Frequency != "" && currentLesson.ActiveTransmitters[s.Frequency] == s.ID {
							delete(currentLesson.ActiveTransmitters, s.Frequency)
						}
						s.Frequency = msg.Frequency
						currentLesson.logEvent(targetID, s.Name, s.Callsign, s.Frequency, "Frequency assigned")
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "remove_frequency":
			if currentLesson != nil && state != nil && state.IsTutor {
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
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				currentLesson.Callsigns = append(currentLesson.Callsigns, msg.Callsign)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "assign_callsign":
			if currentLesson != nil && state != nil {
				currentLesson.mu.Lock()
				canAssign := state.IsTutor
				targetID := msg.StudentID
				if !canAssign {
					if currentLesson.Type == LessonOpen || currentLesson.Type == LessonRestrictedFreq {
						if !currentLesson.CallsignVerify {
							canAssign = true
							targetID = state.UserID
						} else {
							currentLesson.PendingCallsigns = append(currentLesson.PendingCallsigns, CallsignRequest{
								StudentID:   state.UserID,
								StudentName: currentLesson.Students[state.UserID].Name,
								NewCallsign: msg.Callsign,
							})
						}
					}
				}

				if canAssign {
					unique := true
					for _, s := range currentLesson.Students {
						if s.Callsign == msg.Callsign && s.ID != targetID {
							unique = false
							break
						}
					}
					if unique {
						if s, ok := currentLesson.Students[targetID]; ok {
							s.Callsign = msg.Callsign
							currentLesson.logEvent(targetID, s.Name, s.Callsign, s.Frequency, "Callsign assigned")
						}
					} else if !state.IsTutor {
						conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"Callsign already in use"}`))
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "approve_callsign":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
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
			if currentLesson != nil && state != nil && state.IsTutor {
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
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[msg.StudentID]; ok {
					s.Callsign = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clear_frequencies":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "ptt":
			if currentLesson != nil && state != nil {
				currentLesson.mu.Lock()
				if !state.IsTutor {
					if s, ok := currentLesson.Students[state.UserID]; ok {
						if s.Frequency != "" && s.Callsign != "" {
							if msg.IsPTTing {
								if _, busy := currentLesson.ActiveTransmitters[s.Frequency]; !busy {
									currentLesson.ActiveTransmitters[s.Frequency] = state.UserID
									s.IsPTTing = true
								} else {
									s.IsPTTing = false
								}
							} else {
								if s.IsPTTing && currentLesson.ActiveTransmitters[s.Frequency] == state.UserID {
									delete(currentLesson.ActiveTransmitters, s.Frequency)
									// Save recording
									audioData := currentLesson.ActiveRecordings[state.UserID]
									audioFile := currentLesson.saveAudio(state.UserID, audioData)
									transcript := currentLesson.CurrentTranscripts[state.UserID]
									currentLesson.logTransmission(state.UserID, s.Name, s.Callsign, s.Frequency, audioFile, transcript)
									delete(currentLesson.ActiveRecordings, state.UserID)
									delete(currentLesson.CurrentTranscripts, state.UserID)
								}
								s.IsPTTing = false
							}
						} else {
							s.IsPTTing = false
						}
					}
				} else {
					// Tutor PTT
					// Tutor PTT overrides other transmitters on the frequency?
					// Prompt says "when one student is transmitting, no others can".
					// It doesn't explicitly say tutors override, but usually they do.
					// Let's implement Tutor PTT separately in the audio relay logic.
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "tutor_ptt":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				if msg.IsPTTing {
					// Start recording (binary will append)
				} else {
					// Stop and save
					audioData := currentLesson.ActiveRecordings[state.UserID]
					audioFile := currentLesson.saveAudio(state.UserID, audioData)
					transcript := currentLesson.CurrentTranscripts[state.UserID]
					currentLesson.logTransmission(state.UserID, msg.Name, "TUTOR", "GLOBAL", audioFile, transcript)
					delete(currentLesson.ActiveRecordings, state.UserID)
					delete(currentLesson.CurrentTranscripts, state.UserID)
				}
				currentLesson.mu.Unlock()
			}

		case "clean_students":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				forceLeaveAllStudents(currentLesson)
				currentLesson.Students = make(map[string]*Student)
				currentLesson.ActiveTransmitters = make(map[string]string)
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "clean_frequencies":
			if currentLesson != nil && state != nil && state.IsTutor {
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
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				currentLesson.Callsigns = []string{}
				for _, s := range currentLesson.Students {
					s.Callsign = ""
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "end_ex":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				currentLesson.ActiveTransmitters = make(map[string]string)
				for _, s := range currentLesson.Students {
					s.Frequency = ""
				}
				// TTS Broadcast
				ttsMsg := map[string]string{"type": "tts_broadcast", "message": "End of exercise. End of exercise. End of exercise."}
				ttsData, _ := json.Marshal(ttsMsg)
				for c, st := range currentLesson.conns {
					if !st.IsTutor {
						c.WriteMessage(websocket.TextMessage, ttsData)
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "end_lesson":
			if currentLesson != nil && state != nil && state.IsMainTutor {
				log.Printf("End lesson requested for %s", currentLesson.ID)
				zipFile, err := currentLesson.generateZip()
				if err != nil {
					log.Printf("Error generating zip: %v", err)
				}
				currentLesson.mu.Lock()
				currentLesson.IsActive = false
				forceLeaveAllStudents(currentLesson)

				// Tell main tutor to download zip
				if zipFile != "" {
					conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"download_zip","url":"/admin/download?file=`+zipFile+`"}`))
				}
				currentLesson.mu.Unlock()
			}

		case "update_permissions":
			if currentLesson != nil && currentUserID != "" && !state.IsTutor {
				currentLesson.mu.Lock()
				if s, ok := currentLesson.Students[currentUserID]; ok {
					s.HasPermissions = msg.HasPermissions
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}

		case "force_permission_request":
			if currentLesson != nil && state != nil && state.IsTutor {
				currentLesson.mu.Lock()
				targetID := msg.TargetID
				for c, s := range currentLesson.conns {
					if s.UserID == targetID {
						c.WriteMessage(websocket.TextMessage, []byte(`{"type":"force_permission_request"}`))
						break
					}
				}
				currentLesson.mu.Unlock()
			}

		case "transcript":
			if currentLesson != nil && state != nil {
				currentLesson.mu.Lock()
				currentLesson.CurrentTranscripts[state.UserID] = msg.Text
				var callsign, freq string
				if state.IsTutor {
					callsign = "TUTOR"
					freq = "GLOBAL" // or actual freq if we track it
				} else {
					if s, ok := currentLesson.Students[state.UserID]; ok {
						callsign = s.Callsign
						freq = s.Frequency
					}
				}

				// Broadcast transcript to tutors
				msg := map[string]interface{}{
					"type":      "transcript",
					"user_id":   state.UserID,
					"name":      msg.Name,
					"callsign":  callsign,
					"frequency": freq,
					"text":      msg.Text,
				}
				data, _ := json.Marshal(msg)
				for c, st := range currentLesson.conns {
					if st.IsTutor {
						c.WriteMessage(websocket.TextMessage, data)
					}
				}
				currentLesson.mu.Unlock()
			}

		case "role_change":
			if currentLesson != nil && state != nil && state.IsMainTutor {
				currentLesson.mu.Lock()
				targetID := msg.TargetID
				newRole := msg.NewRole

				// Find target connection
				var targetConn *websocket.Conn
				var targetState *ConnState
				for c, s := range currentLesson.conns {
					if s.UserID == targetID {
						targetConn = c
						targetState = s
						break
					}
				}

				if targetConn != nil {
					if newRole == "tutor" && !targetState.IsTutor {
						// Elevate student to tutor
						s := currentLesson.Students[targetID]
						targetState.IsTutor = true
						currentLesson.Tutors[targetID] = s.Name
						delete(currentLesson.Students, targetID)
						targetConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"role_change","new_role":"tutor"}`))
					} else if newRole == "student" && targetState.IsTutor && !targetState.IsMainTutor {
						// Demote tutor to student
						name := currentLesson.Tutors[targetID]
						targetState.IsTutor = false
						delete(currentLesson.Tutors, targetID)
						currentLesson.Students[targetID] = &Student{ID: targetID, Name: name}
						targetConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"role_change","new_role":"student"}`))
					}
				}
				currentLesson.mu.Unlock()
				broadcastUpdate(currentLesson)
			}
		}
	}
}

func forceLeaveAllStudents(l *Lesson) {
	msg := map[string]string{"type": "force_leave"}
	data, _ := json.Marshal(msg)
	for conn, state := range l.conns {
		if state != nil && !state.IsTutor {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

func (l *Lesson) logEvent(userID, name, callsign, freq, details string) {
	l.Logs = append(l.Logs, LogEntry{
		Timestamp: time.Now(),
		Type:      "event",
		UserID:    userID,
		Name:      name,
		Callsign:  callsign,
		Frequency: freq,
		Details:   details,
	})
}

func (l *Lesson) saveAudio(userID string, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	fileName := fmt.Sprintf("logs/%s_%d.wav", userID, time.Now().UnixNano())
	f, err := os.Create(fileName)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Simple WAV header for 1 channel, 44100Hz (typical for web audio)
	// Web Audio sample rate can vary, but let's assume 44100 or 48000.
	// Actually, the client sends raw Float32.
	// For simplicity, let's just write the raw data and a basic header.
	// Realistically, we'd need to know the client's sample rate.
	// Let's assume 44100 for now.
	sampleRate := uint32(44100)

	binary.Write(f, binary.LittleEndian, []byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+len(data)))
	binary.Write(f, binary.LittleEndian, []byte("WAVE"))
	binary.Write(f, binary.LittleEndian, []byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(3)) // IEEE Float
	binary.Write(f, binary.LittleEndian, uint16(1)) // Mono
	binary.Write(f, binary.LittleEndian, sampleRate)
	binary.Write(f, binary.LittleEndian, sampleRate*4)
	binary.Write(f, binary.LittleEndian, uint16(4))
	binary.Write(f, binary.LittleEndian, uint16(32))
	binary.Write(f, binary.LittleEndian, []byte("data"))
	binary.Write(f, binary.LittleEndian, uint32(len(data)))
	f.Write(data)

	return fileName
}

func (l *Lesson) generateZip() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Printf("Generating zip for lesson %s", l.ID)

	zipName := fmt.Sprintf("logs/lesson_%s_%d.zip", l.ID, time.Now().Unix())
	zipFile, err := os.Create(zipName)
	if err != nil {
		return "", err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	// 1. Add log.json
	logData, _ := json.MarshalIndent(l.Logs, "", "  ")
	f, _ := archive.Create("log.json")
	f.Write(logData)

	// 2. Add audio files
	for _, entry := range l.Logs {
		if entry.AudioFile != "" {
			audioData, err := os.ReadFile(entry.AudioFile)
			if err == nil {
				af, _ := archive.Create(entry.AudioFile) //entry.AudioFile starts with logs/
				af.Write(audioData)
			}
		}
	}

	// 3. Add timeline.html (with embedded data)
	timelineTmpl, err := os.ReadFile("templates/timeline.html")
	if err == nil {
		html := strings.Replace(string(timelineTmpl), "/*DATA_PLACEHOLDER*/", "const logData = " + string(logData) + ";", 1)
		tf, _ := archive.Create("timeline.html")
		tf.Write([]byte(html))
	}

	return zipName, nil
}

func (l *Lesson) logTransmission(userID, name, callsign, freq, audioFile, transcript string) {
	l.Logs = append(l.Logs, LogEntry{
		Timestamp:  time.Now(),
		Type:       "transmission",
		UserID:     userID,
		Name:       name,
		Callsign:   callsign,
		Frequency:  freq,
		AudioFile:  audioFile,
		Transcript: transcript,
	})
}

func broadcastUpdate(l *Lesson) {
	l.mu.Lock()
	defer l.mu.Unlock()

	students := make([]*Student, 0, len(l.Students))
	for _, s := range l.Students {
		students = append(students, s)
	}

	tutors := make([]TutorInfo, 0, len(l.Tutors))
	for id, name := range l.Tutors {
		tutors = append(tutors, TutorInfo{ID: id, Name: name})
	}

	msg := Message{
		Type:             "update",
		LessonID:         l.ID,
		Frequencies:      l.Frequencies,
		Callsigns:        l.Callsigns,
		Students:         students,
		Tutors:           tutors,
		MainTutorID:      l.MainTutorID,
		LessonType:       l.Type,
		CallsignVerify:   l.CallsignVerify,
		PendingCallsigns: l.PendingCallsigns,
		TutorName:        l.Tutors[l.MainTutorID],
	}

	data, _ := json.Marshal(msg)

	for conn := range l.conns {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			continue
		}
	}
}
