package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/felixge/fgprof"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const TotalSheets = 1000

type User struct {
	ID        int64  `json:"id,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	LoginName string `json:"login_name,omitempty"`
	PassHash  string `json:"pass_hash,omitempty"`
}

type Event struct {
	ID       int64  `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	PublicFg bool   `json:"public,omitempty"`
	ClosedFg bool   `json:"closed,omitempty"`
	Price    int64  `json:"price,omitempty"`
	SRemains int
	ARemains int
	BRemains int
	CRemains int

	Total   int                `json:"total"`
	Remains int                `json:"remains"`
	Sheets  map[string]*Sheets `json:"sheets,omitempty"`
}

type Sheets struct {
	Total   int      `json:"total"`
	Remains int      `json:"remains"`
	Detail  []*Sheet `json:"detail,omitempty"`
	Price   int64    `json:"price"`
}

type Sheet struct {
	ID    int64  `json:"-"`
	Rank  string `json:"-"`
	Num   int64  `json:"num"`
	Price int64  `json:"-"`

	Mine           bool       `json:"mine,omitempty"`
	Reserved       bool       `json:"reserved,omitempty"`
	ReservedAt     *time.Time `json:"-"`
	ReservedAtUnix int64      `json:"reserved_at,omitempty"`
}

type Reservation struct {
	ID         int64      `json:"id"`
	EventID    int64      `json:"-"`
	SheetID    int64      `json:"-"`
	UserID     int64      `json:"-"`
	ReservedAt *time.Time `json:"-"`
	CanceledAt *time.Time `json:"-"`
	UpdatedAt  *time.Time `json:"-"`

	Event          *Event `json:"event,omitempty"`
	SheetRank      string `json:"sheet_rank,omitempty"`
	SheetNum       int64  `json:"sheet_num,omitempty"`
	Price          int64  `json:"price,omitempty"`
	ReservedAtUnix int64  `json:"reserved_at,omitempty"`
	CanceledAtUnix int64  `json:"canceled_at,omitempty"`
}

type Administrator struct {
	ID        int64  `json:"id,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	LoginName string `json:"login_name,omitempty"`
	PassHash  string `json:"pass_hash,omitempty"`
}

func sessUserID(c echo.Context) int64 {
	sess, _ := session.Get("session", c)
	var userID int64
	if x, ok := sess.Values["user_id"]; ok {
		userID, _ = x.(int64)
	}
	return userID
}

func sessSetUserID(c echo.Context, id int64) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	sess.Values["user_id"] = id
	sess.Save(c.Request(), c.Response())
}

func sessDeleteUserID(c echo.Context) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	delete(sess.Values, "user_id")
	sess.Save(c.Request(), c.Response())
}

func sessAdministratorID(c echo.Context) int64 {
	sess, _ := session.Get("session", c)
	var administratorID int64
	if x, ok := sess.Values["administrator_id"]; ok {
		administratorID, _ = x.(int64)
	}
	return administratorID
}

func sessSetAdministratorID(c echo.Context, id int64) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	sess.Values["administrator_id"] = id
	sess.Save(c.Request(), c.Response())
}

func sessDeleteAdministratorID(c echo.Context) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	delete(sess.Values, "administrator_id")
	sess.Save(c.Request(), c.Response())
}

func loginRequired(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := getLoginUser(c); err != nil {
			return resError(c, "login_required", 401)
		}
		return next(c)
	}
}

func adminLoginRequired(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := getLoginAdministrator(c); err != nil {
			return resError(c, "admin_login_required", 401)
		}
		return next(c)
	}
}

func getLoginUser(c echo.Context) (*User, error) {
	userID := sessUserID(c)
	if userID == 0 {
		return nil, errors.New("not logged in")
	}
	var user User
	err := db.QueryRow("SELECT id, nickname FROM users WHERE id = ?", userID).Scan(&user.ID, &user.Nickname)
	return &user, err
}

func getLoginAdministrator(c echo.Context) (*Administrator, error) {
	administratorID := sessAdministratorID(c)
	if administratorID == 0 {
		return nil, errors.New("not logged in")
	}
	var administrator Administrator
	err := db.QueryRow("SELECT id, nickname FROM administrators WHERE id = ?", administratorID).Scan(&administrator.ID, &administrator.Nickname)
	return &administrator, err
}

func getEvents(all bool) ([]*Event, error) {
	var query string
	if all {
		query = `SELECT * FROM events ORDER BY id ASC`
	} else {
		query = `SELECT * FROM events WHERE public_fg = TRUE ORDER BY id ASC`
	}

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}

	var events []*Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price, &event.SRemains, &event.ARemains, &event.BRemains, &event.CRemains); err != nil {
			return nil, err
		}
		event.Total = TotalSheets
		event.Remains = event.SRemains + event.ARemains + event.BRemains + event.CRemains
		event.Sheets = map[string]*Sheets{
			"S": {
				Total:   sheetsTotal["S"],
				Price:   event.Price + sheetsPrice["S"],
				Remains: event.SRemains,
			},
			"A": {
				Total:   sheetsTotal["A"],
				Price:   event.Price + sheetsPrice["A"],
				Remains: event.ARemains,
			},
			"B": {
				Total:   sheetsTotal["B"],
				Price:   event.Price + sheetsPrice["B"],
				Remains: event.BRemains,
			},
			"C": {
				Total:   sheetsTotal["C"],
				Price:   event.Price + sheetsPrice["C"],
				Remains: event.CRemains,
			},
		}

		events = append(events, &event)
	}

	return events, nil
}

func getEventsOld(all bool) ([]*Event, error) {
	rows, err := db.Query("SELECT * FROM events ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price, &event.SRemains, &event.ARemains, &event.BRemains, &event.CRemains); err != nil {
			return nil, err
		}
		if !all && !event.PublicFg {
			continue
		}
		events = append(events, &event)
	}
	for i, v := range events {
		event, err := getEvent(v.ID, -1)
		if err != nil {
			return nil, err
		}
		for k := range event.Sheets {
			event.Sheets[k].Detail = nil
		}
		events[i] = event
	}
	return events, nil
}

func getEvent(eventID, loginUserID int64) (*Event, error) {
	var event Event
	if err := db.QueryRow("SELECT * FROM events WHERE id = ?", eventID).Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price, &event.SRemains, &event.ARemains, &event.BRemains, &event.CRemains); err != nil {
		return nil, err
	}
	event.Sheets = map[string]*Sheets{
		"S": &Sheets{},
		"A": &Sheets{},
		"B": &Sheets{},
		"C": &Sheets{},
	}

	sheeetIDReservation := map[int64]*Reservation{}
	rows, err := db.Query("SELECT * FROM reservations WHERE event_id = ? AND canceled_at IS NULL GROUP BY event_id, sheet_id HAVING reserved_at = MIN(reserved_at)", event.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var reservation Reservation
		err = rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt)
		if err != nil {
			return nil, err
		}
		sheeetIDReservation[reservation.SheetID] = &reservation
	}

	rows, err = db.Query("SELECT * FROM sheets ORDER BY `rank`, num")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sheet Sheet
		if err := rows.Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			return nil, err
		}
		event.Sheets[sheet.Rank].Price = event.Price + sheet.Price
		event.Total++
		event.Sheets[sheet.Rank].Total++

		if reservation, ok := sheeetIDReservation[sheet.ID]; ok {
			sheet.Mine = reservation.UserID == loginUserID
			sheet.Reserved = true
			sheet.ReservedAtUnix = reservation.ReservedAt.Unix()
		} else {
			event.Remains++
			event.Sheets[sheet.Rank].Remains++
		}

		event.Sheets[sheet.Rank].Detail = append(event.Sheets[sheet.Rank].Detail, &sheet)
	}

	return &event, nil
}

func sanitizeEvent(e *Event) *Event {
	sanitized := *e
	sanitized.Price = 0
	sanitized.PublicFg = false
	sanitized.ClosedFg = false
	return &sanitized
}

func fillinUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if user, err := getLoginUser(c); err == nil {
			c.Set("user", user)
		}
		return next(c)
	}
}

func fillinAdministrator(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if administrator, err := getLoginAdministrator(c); err == nil {
			c.Set("administrator", administrator)
		}
		return next(c)
	}
}

func validateRank(rank string) bool {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM sheets WHERE `rank` = ?", rank).Scan(&count)
	return count > 0
}

type Renderer struct {
	templates *template.Template
}

func (r *Renderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

var db *sql.DB

func main() {
	http.DefaultServeMux.Handle("/debug/fgprof", fgprof.Handler())
	go func() {
		log.Println(http.ListenAndServe(":6060", nil))
	}()

	go func() {
		log.Fatal(http.ListenAndServe(":7070", nil))
	}()

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		os.Getenv("DB_USER"), os.Getenv("DB_PASS"),
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"),
		os.Getenv("DB_DATABASE"),
	)

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	funcs := template.FuncMap{
		"encode_json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}

	e.Renderer = &Renderer{
		templates: template.Must(template.New("").Delims("[[", "]]").Funcs(funcs).ParseGlob("views/*.tmpl")),
	}

	e.Use(session.Middleware(sessions.NewCookieStore([]byte("secret"))))
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{Output: os.Stderr}))
	e.GET("/", func(c echo.Context) error {
		events, err := getEvents(false)
		if err != nil {
			return err
		}
		for i, e := range events {
			events[i] = sanitizeEvent(e)
		}

		return c.Render(200, "index.tmpl", echo.Map{
			"events": events,
			"user":   c.Get("user"),
			"origin": c.Scheme() + "://" + c.Request().Host,
		})
	}, fillinUser)
	e.GET("/initialize", func(c echo.Context) error {
		cmd := exec.Command("../../db/init.sh")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		err := cmd.Run()
		if err != nil {
			return nil
		}

		setRemains()
		setEventsRemains()

		return c.NoContent(204)
	})
	e.POST("/api/users", addUserHandler)
	e.GET("/api/users/:id", getUserHandler, loginRequired)
	e.POST("/api/actions/login", loginHandler)
	e.POST("/api/actions/logout", logoutHandler, loginRequired)
	e.GET("/api/events", getEventsHandler)
	e.GET("/api/events/:id", getEventHandler)
	e.POST("/api/events/:id/actions/reserve", addReservationHandler, loginRequired)
	e.DELETE("/api/events/:id/sheets/:rank/:num/reservation", removeReservationHandler, loginRequired)
	e.GET("/admin/", getAdminHandler, fillinAdministrator)
	e.POST("/admin/api/actions/login", loginAdminHandler)
	e.POST("/admin/api/actions/logout", logoutAdminHandler, adminLoginRequired)
	e.GET("/admin/api/events", getAdminEventsHandler, adminLoginRequired)
	e.POST("/admin/api/events", addAdminEventHandler, adminLoginRequired)
	e.GET("/admin/api/events/:id", getAdminEventHandler, adminLoginRequired)
	e.POST("/admin/api/events/:id/actions/edit", editAdminEventHandler, adminLoginRequired)
	e.GET("/admin/api/reports/events/:id/sales", getReportHandler, adminLoginRequired)
	e.GET("/admin/api/reports/sales", getReportsHandler, adminLoginRequired)

	setSeets()
	setEventsRemains()

	e.Start(":8080")
}

type Report struct {
	ReservationID int64
	EventID       int64
	Rank          string
	Num           int64
	UserID        int64
	SoldAt        string
	CanceledAt    string
	Price         int64
}

func renderReportCSV(c echo.Context, reports []Report) error {
	sort.Slice(reports, func(i, j int) bool { return strings.Compare(reports[i].SoldAt, reports[j].SoldAt) < 0 })

	body := bytes.NewBufferString("reservation_id,event_id,rank,num,price,user_id,sold_at,canceled_at\n")
	for _, v := range reports {
		body.WriteString(fmt.Sprintf("%d,%d,%s,%d,%d,%d,%s,%s\n",
			v.ReservationID, v.EventID, v.Rank, v.Num, v.Price, v.UserID, v.SoldAt, v.CanceledAt))
	}

	c.Response().Header().Set("Content-Type", `text/csv; charset=UTF-8`)
	c.Response().Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
	_, err := io.Copy(c.Response(), body)
	return err
}

func resError(c echo.Context, e string, status int) error {
	if e == "" {
		e = "unknown"
	}
	if status < 100 {
		status = 500
	}
	return c.JSON(status, map[string]string{"error": e})
}

var sheets map[int64]Sheet

var sheetsTotal map[string]int
var sheetsPrice map[string]int64

var eventsRemains map[int64]int

func setEventsRemains() {
	eventsRemains = map[int64]int{}
	rows, _ := db.Query(`SELECT id FROM events`)
	for rows.Next() {
		var eventID int
		rows.Scan(&eventID)
		eventsRemains[int64(eventID)] = TotalSheets
	}

	rows, _ = db.Query(`
	SELECT event_id FROM reservations WHERE canceled_at IS NULL`)
	for rows.Next() {
		var eventID int64
		rows.Scan(&eventID)
		eventsRemains[eventID]--
	}
}

func setSeets() {
	sheets = map[int64]Sheet{}
	sheetsTotal = map[string]int{
		"S": 0,
		"A": 0,
		"B": 0,
		"C": 0,
	}
	sheetsPrice = map[string]int64{
		"S": 0,
		"A": 0,
		"B": 0,
		"C": 0,
	}

	rows, err := db.Query(`SELECT * FROM sheets`)
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	for rows.Next() {
		var s Sheet
		err = rows.Scan(&s.ID, &s.Rank, &s.Num, &s.Price)
		sheets[s.ID] = s

		sheetsTotal[s.Rank]++
		sheetsPrice[s.Rank] = s.Price
	}

	err = rows.Close()
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
}

func makeEvent(event Event, userID int64) (*Event, error) {
	event.Sheets = map[string]*Sheets{
		"S": {
			Total:   sheetsTotal["S"],
			Price:   event.Price + sheetsPrice["S"],
			Remains: sheetsTotal["S"],
		},
		"A": {
			Total:   sheetsTotal["A"],
			Price:   event.Price + sheetsPrice["A"],
			Remains: sheetsTotal["A"],
		},
		"B": {
			Total:   sheetsTotal["B"],
			Price:   event.Price + sheetsPrice["B"],
			Remains: sheetsTotal["B"],
		},
		"C": {
			Total:   sheetsTotal["C"],
			Price:   event.Price + sheetsPrice["C"],
			Remains: sheetsTotal["C"],
		},
	}

	rows, err := db.Query("SELECT * FROM reservations WHERE event_id = ? AND canceled_at IS NULL", event.ID)
	if err != nil {
		return nil, err
	}

	addedSheetsIDs := map[int64]struct{}{}

	for rows.Next() {
		var reservation Reservation
		err = rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt)
		if err != nil {
			return nil, err
		}

		sheet := sheets[reservation.SheetID]
		sheet.Mine = reservation.UserID == userID
		sheet.Reserved = true
		sheet.ReservedAtUnix = reservation.ReservedAt.Unix()

		event.Sheets[sheet.Rank].Detail = append(event.Sheets[sheet.Rank].Detail, &sheet)
		event.Sheets[sheet.Rank].Remains--
		event.Total++

		addedSheetsIDs[reservation.SheetID] = struct{}{}
	}

	for _, sheet := range sheets {
		if _, ok := addedSheetsIDs[sheet.ID]; !ok {
			event.Sheets[sheet.Rank].Detail = append(event.Sheets[sheet.Rank].Detail, &sheet)
			event.Total++
			event.Remains++
		}
	}

	return &event, nil
}

func setRemains() {
	events, err := getEventsOld(true)
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	for _, e := range events {
		_, err := db.Exec(`
		UPDATE events
		SET s_remains = ?, a_remains = ?, b_remains = ?, c_remains = ?
		WHERE id = ?`,
			e.Sheets["S"].Remains, e.Sheets["A"].Remains, e.Sheets["B"].Remains, e.Sheets["C"].Remains,
			e.ID)
		if err != nil {
			fmt.Println(err)
			panic(err)
		}
	}
}
