package main

import (
	"adat/access"
	"adat/pgarray"
	"adat/uuid"
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"time"
)

var dashboardsRouter = &Transactional{PrefixRouter(map[string]Handler{
	"/": MethodRouter(map[string]Handler{
		"GET":  HandlerFunc(listDashboards),
		"POST": HandlerFunc(createDashboard),
	}),
	"*uuid": MethodRouter(map[string]Handler{
		"GET":    HandlerFunc(getDashboard),
		"PUT":    HandlerFunc(changeDashboard),
		"PATHCH": HandlerFunc(changeDashboard),
		"DELETE": HandlerFunc(deleteDashboard),
	}),
})}

func listDashboards(t *Task) {
	if !access.HasPermission(t.Tx, t.Uid, "GET", "dashboards", "") {
		t.Rw.WriteHeader(http.StatusForbidden)
		return
	}

	rows, err := t.Tx.Query(`
		SELECT
			d.id,
			d.title,
			d.slug,
			d.category,
			d.position,
			d.created,
			d.creator,
			array_agg(w.id)
		FROM
			dashboards d
		LEFT JOIN
			widgets w
		ON
			w.dashboard = d.id
		GROUP BY d.id
		ORDER BY
			d.position`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	dashboards := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, title, slug, category, creator string
		var position int
		var created time.Time
		var widgets pgarray.StringSlice
		err := rows.Scan(&id, &title, &slug, &category, &position, &created,
			&creator, &widgets)
		if err != nil {
			panic(err)
		}

		dashboards = append(dashboards, map[string]interface{}{
			"id":       id,
			"title":    title,
			"category": category,
			"position": position,
			"created":  created.Format("2006-01-02 15:04:05"),
			"creator":  creator,
			"widgets":  widgets,
		})
	}

	t.SendJsonObject("dashboards", dashboards)
}

func createDashboard(t *Task) {
	if !access.HasPermission(t.Tx, t.Uid, "POST", "dashboards", "") {
		t.Rw.WriteHeader(http.StatusForbidden)
		return
	}

	data, ok := t.RecvJson().(map[string]interface{})
	if !ok {
		t.Rw.WriteHeader(http.StatusBadRequest)
		return
	}

	data = data["dashboard"].(map[string]interface{})

	log.Println(data)

	title, ok := data["title"].(string)
	if !ok || title == "" {
		t.SendError("'title' is required")
		return
	}

	category, ok := data["category"].(string)
	if !ok || category == "" {
		t.SendError("'category' is required")
		return
	}

	slug, ok := data["slug"].(string)
	if !ok || slug == "" {
		slug = urlizerRegexp.ReplaceAllString(title, "-")
		realSlug := slug
		for n := 1; dashboardSlugUsed(t.Tx, realSlug); n++ {
			realSlug = slug + "-" + strconv.Itoa(n)
		}
		slug = realSlug
	} else {
		if !slugRegexp.MatchString(slug) {
			t.SendError("Invalid characters in slug")
			return
		}
		if dashboardSlugUsed(t.Tx, slug) {
			t.SendError("Slug already in use")
			return
		}
	}

	id, err := uuid.New4()
	if err != nil {
		panic(err)
	}

	position := 1
	row := t.Tx.QueryRow(`
		SELECT COUNT(*)+1 FROM "dashboards"
		WHERE category = $1`, category)
	if err := row.Scan(&position); err != nil {
		panic(err)
	}

	_, err = t.Tx.Exec(`
		INSERT INTO "dashboards" (
			"id",
			"title",
			"slug",
			"category",
			"position",
			"created",
			"creator"
		) VALUES ($1, $2, $3, $4, $5, NOW(), $6)`,
		id, title, slug, category, position, t.Uid)
	if err != nil {
		panic(err)
	}

	t.Rw.WriteHeader(http.StatusCreated)
	sendDashboard(t, id)
}

func getDashboard(t *Task) {
	if !access.HasPermission(t.Tx, t.Uid, "GET", "dashboard", t.UUID) {
		t.Rw.WriteHeader(http.StatusForbidden)
		return
	}

	if !dashboardExists(t.Tx, t.UUID) {
		t.Rw.WriteHeader(http.StatusNotFound)
		return
	}
	sendDashboard(t, t.UUID)
}

func sendDashboard(t *Task, uuid string) {
	row := t.Tx.QueryRow(`
		SELECT
			d.id,
			d.title,
			d.slug,
			d.category,
			d.position,
			d.created,
			d.creator,
			array_agg(w.id)
		FROM
			dashboards d
		LEFT JOIN
			widgets w
		ON
			w.dashboard = d.id
		WHERE
			d.id = $1
		GROUP BY d.id`, uuid)

	var id, title, slug, category, creator string
	var position int
	var created time.Time
	var widgets pgarray.StringSlice
	err := row.Scan(&id, &title, &slug, &category, &position, &created,
		&creator, &widgets)
	if err != nil {
		panic(err)
	}

	dashboard := map[string]interface{}{
		"id":       id,
		"title":    title,
		"slug":     slug,
		"category": category,
		"position": position,
		"created":  created.Format("2006-01-02 15:04:05"),
		"creator":  creator,
		"widgets":  widgets,
	}

	t.SendJsonObject("dashboard", dashboard)
}

func changeDashboard(t *Task) {
	if !access.HasPermission(t.Tx, t.Uid, "PATCH", "dashboard", t.UUID) {
		t.Rw.WriteHeader(http.StatusForbidden)
		return
	}

	if !dashboardExists(t.Tx, t.UUID) {
		t.Rw.WriteHeader(http.StatusNotFound)
		return
	}

	rawData, ok := t.RecvJson().(map[string]interface{})
	if !ok {
		t.Rw.WriteHeader(http.StatusBadRequest)
		return
	}

	data := rawData["dashboard"].(map[string]interface{})

	fields := map[string]interface{}{}

	if title, ok := data["title"].(string); ok {
		if title == "" {
			t.SendError("'title' is required")
			return
		}
		fields["title"] = title
	}

	if slug, ok := data["slug"].(string); ok {
		if slug == "" {
			t.SendError("'slug' must not be empty")
			return
		}
		if dashboardSlugUsed(t.Tx, slug) {
			t.SendError("'slug' already in use")
			return
		}
		if !slugRegexp.MatchString(slug) {
			t.SendError("Invalid characters in 'slug'")
			return
		}
		fields["slug"] = slug
	}

	category, cok := data["category"].(string)
	positionFlt, pok := data["position"].(float64)
	position := int(positionFlt)

	if cok && category == "" {
		t.SendError("'category' must not be empty")
		return
	}

	if cok || pok {
		row := t.Tx.QueryRow(`
			SELECT "category", "position" FROM "dashboards" WHERE "id" = $1`,
			t.UUID)
		oldCat, oldPos := "", 0
		if err := row.Scan(&oldCat, &oldPos); err != nil {
			panic(err)
		}

		if !pok {
			position = 2e9
		}

		n, row := 0, t.Tx.QueryRow(`
			SELECT COUNT(*)+1 FROM "dashboards"
			WHERE category = $1 AND id != $2`,
			category, t.UUID)
		if err := row.Scan(&n); err != nil {
			panic(err)
		}

		if position < 1 {
			position = 1
		} else if position > n {
			position = n
		}

		if cok && category != oldCat {
			_, err := t.Tx.Exec(`
				UPDATE "dashboards" SET "position" = "position" - 1
				WHERE "category" = $1 AND "position" > $2`, oldCat, oldPos)
			if err != nil {
				panic(err)
			}
			_, err = t.Tx.Exec(`
				UPDATE "dashboards" SET "position" = "position" + 1
				WHERE "category" = $1 AND "position" >= $2`, category, position)
			if err != nil {
				panic(err)
			}
			fields["position"] = position
			fields["category"] = category
		} else if position != oldPos {
			d, min, max := 1, position, oldPos
			if position > oldPos {
				d, min, max = -1, oldPos, position
			}
			_, err := t.Tx.Exec(`
				UPDATE "dashboards" SET "position" = "position" + $1
				WHERE "category" = $2 AND "position" BETWEEN $3 AND $4`,
				d, oldCat, min, max)
			if err != nil {
				panic(err)
			}
			fields["position"] = position
		}
	}

	if len(fields) > 0 {
		set, vals := setClause(fields, t.UUID)
		_, err := t.Tx.Exec(`UPDATE "dashboards" `+set+` WHERE "id" = $1`,
			vals...)
		if err != nil {
			panic(err)
		}
	}
	sendDashboard(t, t.UUID)
}

func deleteDashboard(t *Task) {
	if !access.HasPermission(t.Tx, t.Uid, "DELETE", "dashboard", t.UUID) {
		t.Rw.WriteHeader(http.StatusForbidden)
		return
	}

	if !dashboardExists(t.Tx, t.UUID) {
		t.Rw.WriteHeader(http.StatusNotFound)
		return
	}

	_, err := t.Tx.Exec(`DELETE FROM "dashboards" WHERE "id" = $1`, t.UUID)
	if err != nil {
		panic(err)
	}

	_, err = t.Tx.Exec(`
		DELETE FROM "permissions"
		WHERE "object_type" = 'dashboard' AND "object_id" = $1`,
		t.UUID)
	if err != nil {
		panic(err)
	}
}

func dashboardExists(tx *sql.Tx, id string) bool {
	if !uuid.Valid(id) {
		return false
	}
	row := tx.QueryRow(`
		SELECT COUNT(*) from "dashboards" WHERE id = $1`,
		id)
	n := 0
	if err := row.Scan(&n); err != nil {
		panic(err)
	}
	return n > 0

}

func dashboardSlugUsed(tx *sql.Tx, slug string) bool {
	row := tx.QueryRow(`
		SELECT COUNT(*) FROM "dashboards" WHERE "slug" = $1`,
		slug)
	n := 0
	if err := row.Scan(&n); err != nil {
		panic(err)
	}
	return n > 0
}
