package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"ssb-backend/internal/db"
	"ssb-backend/internal/domain"
)

// postChat answers a manager's question from Ebb's own data ("Ask Ebb").
// The LLM only phrases the answer: all numbers come from the context built here.
// Provider: Gemini (env GEMINI_API_KEY, GEMINI_MODEL); swap for a STACKIT-hosted model in production.
func (s *Server) postChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Question) == "" {
		fail(w, 400, fmt.Errorf("question required"))
		return
	}
	if s.GeminiKey == "" {
		fail(w, 501, fmt.Errorf("chat not configured (GEMINI_API_KEY missing)"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	facts, err := s.chatContext(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	prompt := "You are Ebb, a crowd-forecasting assistant for businesses in Heilbronn's city centre.\n" +
		"Answer the question using ONLY the data below. Be concise (max 6 sentences), give concrete numbers and times, " +
		"and say when the data does not cover the question. 'Density' = people within ~10 m of a sensor.\n\n" +
		facts + "\nQUESTION: " + req.Question
	answer, err := gemini(ctx, s.GeminiKey, prompt)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, map[string]string{"answer": answer})
}

func (s *Server) chatContext(ctx context.Context) (string, error) {
	st, err := s.now(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CURRENT TIME: %s\n\nFORECAST (avg density per 3-hour block, next 48 h):\n", st.SimNow.Format("Mon 02 Jan 15:04"))
	rows, err := s.futureForecast(ctx, st.SimNow, nil)
	if err == nil {
		type key struct {
			label string
			t     time.Time
		}
		agg, n := map[key]float64{}, map[key]int{}
		for _, x := range rows {
			if x.ts.After(st.SimNow.Add(48 * time.Hour)) {
				continue
			}
			k := key{x.label, time.Date(x.ts.Year(), x.ts.Month(), x.ts.Day(), x.ts.Hour()/3*3, 0, 0, 0, db.Berlin)}
			agg[k] += x.p50
			n[k]++
		}
		keys := make([]key, 0, len(agg))
		for k := range agg {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].label != keys[j].label {
				return keys[i].label < keys[j].label
			}
			return keys[i].t.Before(keys[j].t)
		})
		last := ""
		for _, k := range keys {
			if k.label != last {
				fmt.Fprintf(&b, "\n%s:", k.label)
				last = k.label
			}
			fmt.Fprintf(&b, " %s %.0f;", k.t.Format("Mon15h"), agg[k]/float64(n[k]))
		}
		b.WriteString("\n")
	}
	srows, err := s.P.Query(ctx, `SELECT id FROM stores`)
	if err == nil {
		var ids []string
		for srows.Next() {
			var id string
			if srows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		srows.Close()
		for _, id := range ids {
			stv, err := s.loadStore(ctx, id)
			if err != nil {
				continue
			}
			req, _ := http.NewRequest("GET", "/?", nil)
			day, _ := pickDay(req, stv.Config, st.SimNow)
			dens, _ := s.dayDensity(ctx, stv.SensorID, day, st.SimNow)
			avgV, avgBake, _ := s.normalWeek(ctx, stv)
			hours, k := domain.Plan(stv.Config, db.Weekday(day), dens, avgV, avgBake)
			fmt.Fprintf(&b, "\nSTORE %s (%s): visitors %.0f, peak %02d:00 (%.0f/h), cashiers by hour:", stv.Name, day.Format("Mon 02 Jan"),
				k.VisitorsTotal, k.PeakHour, k.PeakVisitors)
			for _, h := range hours {
				if h.Open {
					fmt.Fprintf(&b, " %02d:%d", h.Hour, h.Cashiers)
				}
			}
			fmt.Fprintf(&b, "; staff-hours Ebb %d vs fixed %d; bake items %d (waste ~%.0f)\n", k.StaffHoursEbb, k.StaffHoursFixed, k.BakeItemsEbb, k.WasteEbb)
		}
	}
	arows, err := s.P.Query(ctx, `SELECT title, coalesce(detail,'') FROM alerts WHERE ts <= $1 AND status='open' ORDER BY ts DESC LIMIT 8`, st.SimNow)
	if err == nil {
		b.WriteString("\nOPEN ALERTS:\n")
		for arows.Next() {
			var t, d string
			if arows.Scan(&t, &d) == nil {
				fmt.Fprintf(&b, "- %s: %s\n", t, d)
			}
		}
		arows.Close()
	}
	return b.String(), nil
}

func gemini(ctx context.Context, key, prompt string) (string, error) {
	model := db.Or(os.Getenv("GEMINI_MODEL"), "gemini-2.5-flash")
	body, _ := json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}},
		"generationConfig": map[string]any{"temperature": 0.2, "maxOutputTokens": 600},
	})
	url := "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("gemini: %s", out.Error.Message)
	}
	var sb strings.Builder
	for _, c := range out.Candidates {
		for _, p := range c.Content.Parts {
			sb.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
