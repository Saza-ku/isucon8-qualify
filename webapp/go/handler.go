package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

func addUserHandler(c echo.Context) error {
	var params struct {
		Nickname  string `json:"nickname"`
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	c.Bind(&params)

	var user User
	if err := db.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != sql.ErrNoRows {
		if err == nil {
			return resError(c, "duplicated", 409)
		}
		return err
	}

	res, err := db.Exec("INSERT INTO users (login_name, pass_hash, nickname) VALUES (?, SHA2(?, 256), ?)", params.LoginName, params.Password, params.Nickname)
	if err != nil {
		return resError(c, "", 0)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return resError(c, "", 0)
	}

	return c.JSON(201, echo.Map{
		"id":       userID,
		"nickname": params.Nickname,
	})
}

func getUserHandler(c echo.Context) error {
	var user User
	if err := db.QueryRow("SELECT id, nickname FROM users WHERE id = ?", c.Param("id")).Scan(&user.ID, &user.Nickname); err != nil {
		return err
	}

	loginUser, err := getLoginUser(c)
	if err != nil {
		return err
	}
	if user.ID != loginUser.ID {
		return resError(c, "forbidden", 403)
	}

	rows, err := db.Query(`
	SELECT * FROM reservations r
	JOIN events e
	ON r.event_id = e.id
	WHERE r.user_id = ? 
	ORDER BY r.updated_at DESC
	LIMIT 5`, user.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var recentReservations []Reservation
	for rows.Next() {
		var reservation Reservation
		var event Event
		if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt,
			&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price, &event.SRemains, &event.ARemains, &event.BRemains, &event.CRemains); err != nil {
			return err
		}
		sheet := sheets[reservation.SheetID]

		event.Sheets = nil
		event.Total = 0
		event.Remains = 0

		e, err := makeEvent(event, reservation.UserID)
		if err != nil {
			return err
		}

		reservation.Event = e
		reservation.SheetRank = sheet.Rank
		reservation.SheetNum = sheet.Num
		reservation.ReservedAtUnix = reservation.ReservedAt.Unix()
		if reservation.CanceledAt != nil {
			reservation.CanceledAtUnix = reservation.CanceledAt.Unix()
		}
		recentReservations = append(recentReservations, reservation)
	}
	if recentReservations == nil {
		recentReservations = make([]Reservation, 0)
	}

	var totalPrice int
	if err := db.QueryRow("SELECT IFNULL(SUM(price), 0) FROM reservations WHERE user_id = ? AND canceled_at IS NULL", user.ID).Scan(&totalPrice); err != nil {
		return err
	}

	rows, err = db.Query(`
	SELECT e.*
	FROM reservations r
	JOIN events e
	ON e.id = r.event_id
	WHERE r.user_id = ? GROUP BY r.event_id ORDER BY MAX(r.updated_at) DESC LIMIT 5`, user.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var recentEvents []*Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price, &event.SRemains, &event.ARemains, &event.BRemains, &event.CRemains); err != nil {
			return err
		}
		e, err := makeEvent(event, -1)
		if err != nil {
			return err
		}
		for k := range e.Sheets {
			e.Sheets[k].Detail = nil
		}
		recentEvents = append(recentEvents, e)
	}
	if recentEvents == nil {
		recentEvents = make([]*Event, 0)
	}

	return c.JSON(200, echo.Map{
		"id":                  user.ID,
		"nickname":            user.Nickname,
		"recent_reservations": recentReservations,
		"total_price":         totalPrice,
		"recent_events":       recentEvents,
	})
}

func loginHandler(c echo.Context) error {
	var params struct {
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	c.Bind(&params)

	user := new(User)
	if err := db.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "authentication_failed", 401)
		}
		return err
	}

	var passHash string
	if err := db.QueryRow("SELECT SHA2(?, 256)", params.Password).Scan(&passHash); err != nil {
		return err
	}
	if user.PassHash != passHash {
		return resError(c, "authentication_failed", 401)
	}

	sessSetUserID(c, user.ID)
	var err error
	user, err = getLoginUser(c)
	if err != nil {
		return err
	}
	return c.JSON(200, user)
}

func logoutHandler(c echo.Context) error {
	sessDeleteUserID(c)
	return c.NoContent(204)
}

func getEventsHandler(c echo.Context) error {
	events, err := getEvents(true)
	if err != nil {
		return err
	}
	for i, v := range events {
		events[i] = sanitizeEvent(v)
	}
	return c.JSON(200, events)
}

func getEventHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}

	loginUserID := int64(-1)
	if user, err := getLoginUser(c); err == nil {
		loginUserID = user.ID
	}

	event, err := getEvent(eventID, loginUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "not_found", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "not_found", 404)
	}
	return c.JSON(200, sanitizeEvent(event))
}

func addReservationHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}
	var params struct {
		Rank string `json:"sheet_rank"`
	}
	c.Bind(&params)

	user, err := getLoginUser(c)
	if err != nil {
		return err
	}

	event, err := getEvent(eventID, user.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_event", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "invalid_event", 404)
	}

	if !validateRank(params.Rank) {
		return resError(c, "invalid_rank", 400)
	}

	var sheet Sheet
	var reservationID int64
	for {
		if err := db.QueryRow("SELECT * FROM sheets WHERE id NOT IN (SELECT sheet_id FROM reservations WHERE event_id = ? AND canceled_at IS NULL FOR UPDATE) AND `rank` = ? ORDER BY RAND() LIMIT 1", event.ID, params.Rank).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "sold_out", 409)
			}
			return err
		}

		res, err := db.Exec("INSERT INTO reservations (event_id, sheet_id, user_id, reserved_at, price) VALUES (?, ?, ?, ?, ?)",
			event.ID, sheet.ID, user.ID, time.Now().UTC().Format("2006-01-02 15:04:05.000000"), sheet.Price+event.Price)
		if err != nil {
			log.Println("re-try: rollback by", err)
			continue
		}
		reservationID, err = res.LastInsertId()
		if err != nil {
			log.Println("re-try: rollback by", err)
			continue
		}

		eventsRemains[eventID]--
		decrementRemains(strings.ToLower(sheet.Rank)+"_remains", int(eventID))

		break
	}
	return c.JSON(202, echo.Map{
		"id":         reservationID,
		"sheet_rank": params.Rank,
		"sheet_num":  sheet.Num,
	})
}

func removeReservationHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}
	rank := c.Param("rank")
	num := c.Param("num")

	user, err := getLoginUser(c)
	if err != nil {
		return err
	}

	event, err := getEvent(eventID, user.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_event", 404)
		}
		return err
	} else if !event.PublicFg {
		return resError(c, "invalid_event", 404)
	}

	if !validateRank(rank) {
		return resError(c, "invalid_rank", 404)
	}

	var sheet Sheet
	if err := db.QueryRow("SELECT * FROM sheets WHERE `rank` = ? AND num = ?", rank, num).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "invalid_sheet", 404)
		}
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	var reservation Reservation
	if err := tx.QueryRow("SELECT * FROM reservations WHERE event_id = ? AND sheet_id = ? AND canceled_at IS NULL GROUP BY event_id HAVING reserved_at = MIN(reserved_at) FOR UPDATE", event.ID, sheet.ID).Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt); err != nil {
		tx.Rollback()
		if err == sql.ErrNoRows {
			return resError(c, "not_reserved", 400)
		}
		return err
	}
	if reservation.UserID != user.ID {
		tx.Rollback()
		return resError(c, "not_permitted", 403)
	}

	if _, err := tx.Exec("UPDATE reservations SET canceled_at = ? WHERE id = ?", time.Now().UTC().Format("2006-01-02 15:04:05.000000"), reservation.ID); err != nil {
		tx.Rollback()
		return err
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return err
	}

	eventsRemains[eventID]++
	incrementRemains(strings.ToLower(sheet.Rank)+"_remains", int(eventID))

	return c.NoContent(204)
}

func getAdminHandler(c echo.Context) error {
	var events []*Event
	administrator := c.Get("administrator")
	if administrator != nil {
		var err error
		if events, err = getEvents(true); err != nil {
			return err
		}
	}
	return c.Render(200, "admin.tmpl", echo.Map{
		"events":        events,
		"administrator": administrator,
		"origin":        c.Scheme() + "://" + c.Request().Host,
	})
}

func loginAdminHandler(c echo.Context) error {
	var params struct {
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	c.Bind(&params)

	administrator := new(Administrator)
	if err := db.QueryRow("SELECT * FROM administrators WHERE login_name = ?", params.LoginName).Scan(&administrator.ID, &administrator.LoginName, &administrator.Nickname, &administrator.PassHash); err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "authentication_failed", 401)
		}
		return err
	}

	var passHash string
	if err := db.QueryRow("SELECT SHA2(?, 256)", params.Password).Scan(&passHash); err != nil {
		return err
	}
	if administrator.PassHash != passHash {
		return resError(c, "authentication_failed", 401)
	}

	sessSetAdministratorID(c, administrator.ID)
	var err error
	administrator, err = getLoginAdministrator(c)
	if err != nil {
		return err
	}
	return c.JSON(200, administrator)
}

func logoutAdminHandler(c echo.Context) error {
	sessDeleteAdministratorID(c)
	return c.NoContent(204)
}

func getAdminEventsHandler(c echo.Context) error {
	events, err := getEvents(true)
	if err != nil {
		return err
	}
	return c.JSON(200, events)
}

func addAdminEventHandler(c echo.Context) error {
	var params struct {
		Title  string `json:"title"`
		Public bool   `json:"public"`
		Price  int    `json:"price"`
	}
	c.Bind(&params)

	res, err := db.Exec("INSERT INTO events (title, public_fg, closed_fg, price, s_remains, a_remains, b_remains, c_remains) VALUES (?, ?, 0, ?, 50, 150, 300, 500)", params.Title, params.Public, params.Price)
	if err != nil {
		return err
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	event, err := getEvent(eventID, -1)
	if err != nil {
		return err
	}

	eventsRemains[eventID] = TotalSheets

	return c.JSON(200, event)
}

func getAdminEventHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}
	event, err := getEvent(eventID, -1)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "not_found", 404)
		}
		return err
	}
	return c.JSON(200, event)
}

func editAdminEventHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}

	var params struct {
		Public bool `json:"public"`
		Closed bool `json:"closed"`
	}
	c.Bind(&params)
	if params.Closed {
		params.Public = false
	}

	event, err := getEvent(eventID, -1)
	if err != nil {
		if err == sql.ErrNoRows {
			return resError(c, "not_found", 404)
		}
		return err
	}

	if event.ClosedFg {
		return resError(c, "cannot_edit_closed_event", 400)
	} else if event.PublicFg && params.Closed {
		return resError(c, "cannot_close_public_event", 400)
	}

	if _, err := db.Exec("UPDATE events SET public_fg = ?, closed_fg = ? WHERE id = ?", params.Public, params.Closed, event.ID); err != nil {
		return err
	}

	e, err := getEvent(eventID, -1)
	if err != nil {
		return err
	}
	c.JSON(200, e)
	return nil
}

func getReportHandler(c echo.Context) error {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return resError(c, "not_found", 404)
	}

	rows, err := db.Query("SELECT * FROM reservations WHERE event_id = ? ORDER BY reserved_at ASC", eventID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var reservation Reservation
		if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt); err != nil {
			return err
		}
		sheet := sheets[reservation.SheetID]
		report := Report{
			ReservationID: reservation.ID,
			EventID:       eventID,
			Rank:          sheet.Rank,
			Num:           sheet.Num,
			UserID:        reservation.UserID,
			SoldAt:        reservation.ReservedAt.Format("2006-01-02T15:04:05.000000Z"),
			Price:         reservation.Price,
		}
		if reservation.CanceledAt != nil {
			report.CanceledAt = reservation.CanceledAt.Format("2006-01-02T15:04:05.000000Z")
		}
		reports = append(reports, report)
	}
	return renderReportCSV(c, reports)
}

func getReportsHandler(c echo.Context) error {
	rows, err := db.Query(`SELECT * FROM reservations ORDER BY reserved_at asc`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var reservation Reservation
		if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &reservation.Price, &reservation.UpdatedAt); err != nil {
			return err
		}
		sheet := sheets[reservation.SheetID]
		report := Report{
			ReservationID: reservation.ID,
			EventID:       reservation.EventID,
			Rank:          sheet.Rank,
			Num:           sheet.Num,
			UserID:        reservation.UserID,
			SoldAt:        reservation.ReservedAt.Format("2006-01-02T15:04:05.000000Z"),
			Price:         reservation.Price,
		}
		if reservation.CanceledAt != nil {
			report.CanceledAt = reservation.CanceledAt.Format("2006-01-02T15:04:05.000000Z")
		}
		reports = append(reports, report)
	}
	return renderReportCSV(c, reports)
}

func decrementRemains(row string, id int) {
	db.Exec(fmt.Sprintf(`UPDATE events SET %s = %s - 1 WHERE id = ?`, row, row), id)
}

func incrementRemains(row string, id int) {
	db.Exec(fmt.Sprintf(`UPDATE events SET %s = %s + 1 WHERE id = ?`, row, row), id)
}
