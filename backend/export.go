package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

const maxExportRows = 200000

type exportRow struct {
	Time        time.Time `json:"time"`
	SensorID    string    `json:"sensor_id"`
	Type        string    `json:"type"`
	Value       *float64  `json:"value"`
	QualityFlag *int      `json:"quality_flag,omitempty"` // raw export only
	NReadings   *int      `json:"n_readings,omitempty"`   // aggregated export only
}

// GET /api/export?format=csv|json&from&to&agg=raw|15m|1h|1d&sensor_id=&type=
func (a *app) export(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()

	format := q.Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return badRequest("format must be csv or json")
	}
	aggName := q.Get("agg")
	if aggName == "" {
		aggName = "15m"
	}
	raw := aggName == "raw"
	ag, aggOK := aggs[aggName]
	if !raw && !aggOK {
		return badRequest("agg must be raw, 15m, 1h or 1d")
	}
	metric := q.Get("type")
	if metric != "" && metric != "all" {
		var err error
		if metric, err = parseMetric(metric); err != nil {
			return err
		}
	}
	sensor := q.Get("sensor_id")
	includeTest := parseBool(q.Get("include_test"), false)

	from, to, err := a.timeRange(r, week)
	if err != nil {
		return err
	}

	sq := &qb{}
	if raw {
		sq.add(`SELECT r.time, r.sensor_id, r.type::text, r.value, r.quality_flag, NULL::bigint`)
	} else {
		sq.add(`SELECT time_bucket(` + sq.p(ag.sql) + `::text::interval, r.time) AS bucket, r.sensor_id, r.type::text,
		        avg(r.value) FILTER (WHERE r.quality_flag = 0), NULL::smallint, count(r.value)`)
	}
	sq.add(`FROM readings r JOIN sensors s USING (sensor_id)
		WHERE r.time >= ` + sq.p(from) + ` AND r.time < ` + sq.p(to))
	if sensor != "" {
		sq.add(`AND r.sensor_id = ` + sq.p(sensor))
	}
	if metric != "" && metric != "all" {
		sq.add(`AND r.type = ` + sq.p(metric) + `::text::metric_type`)
	}
	if !includeTest {
		sq.add(`AND NOT s.is_test`)
	}
	if raw {
		sq.add(`ORDER BY r.sensor_id, r.type, r.time`)
	} else {
		sq.add(`GROUP BY bucket, r.sensor_id, r.type ORDER BY r.sensor_id, r.type, bucket`)
	}
	sq.add(`LIMIT ` + sq.p(maxExportRows+1))

	rows, err := a.db.Query(ctx, sq.String(), sq.args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	// The result is collected first, so that a too large export can still be refused with a clear error.
	var out []exportRow
	for rows.Next() {
		var e exportRow
		var flag *int16
		var nRaw *int64
		var nAgg *int64
		if raw {
			if err := rows.Scan(&e.Time, &e.SensorID, &e.Type, &e.Value, &flag, &nRaw); err != nil {
				return err
			}
			if flag != nil {
				f := int(*flag)
				e.QualityFlag = &f
			}
		} else {
			if err := rows.Scan(&e.Time, &e.SensorID, &e.Type, &e.Value, &flag, &nAgg); err != nil {
				return err
			}
			if nAgg != nil {
				n := int(*nAgg)
				e.NReadings = &n
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(out) > maxExportRows {
		return badRequest("export is limited to %d rows; use a shorter range, one sensor_id, or a larger agg", maxExportRows)
	}

	filename := "export-" + aggName + "-" + from.Format("2006-01-02") + "_" + to.Format("2006-01-02")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.`+format+`"`)

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if out == nil {
			out = []exportRow{}
		}
		return enc.Encode(out)
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	lastCol := "n_readings"
	if raw {
		lastCol = "quality_flag"
	}
	cw.Write([]string{"time", "sensor_id", "type", "value", lastCol})
	for _, e := range out {
		val := ""
		if e.Value != nil {
			val = strconv.FormatFloat(*e.Value, 'f', -1, 64)
		}
		last := ""
		if e.QualityFlag != nil {
			last = strconv.Itoa(*e.QualityFlag)
		}
		if e.NReadings != nil {
			last = strconv.Itoa(*e.NReadings)
		}
		cw.Write([]string{e.Time.UTC().Format(time.RFC3339Nano), e.SensorID, e.Type, val, last})
	}
	cw.Flush()
	return cw.Error()
}
