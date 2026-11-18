// Package database provides persistence for matches, scores, and the
// leaderboard. Two implementations are provided:
//
//   - PostgresStore: production implementation backed by PostgreSQL
//     (used by docker-compose).
//   - MemoryStore:   a thread-safe in-memory store used for local
//     development / testing when DATABASE_URL is not set.
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// Score is the live score payload for a match.
type Score struct {
	MatchID     int       `json:"match_id"`
	Team1       string    `json:"team1"`
	Team2       string    `json:"team2"`
	Runs        int       `json:"runs"`
	Wickets     int       `json:"wickets"`
	Overs       string    `json:"overs"`
	Status      string    `json:"status"`
	LastUpdated time.Time `json:"last_updated"`
}

// Match is static match metadata.
type Match struct {
	ID        int    `json:"id"`
	Team1     string `json:"team1"`
	Team2     string `json:"team2"`
	Venue     string `json:"venue"`
	MatchDate string `json:"match_date"`
	Status    string `json:"status"`
}

// Standing is a leaderboard row.
type Standing struct {
	Team      string  `json:"team"`
	Played    int     `json:"played"`
	Won       int     `json:"won"`
	Lost      int     `json:"lost"`
	NRR       float64 `json:"nrr"`
	Points    int     `json:"points"`
}

// QueryFunc is invoked for every real database query; used by metrics.
type QueryFunc func(query string, duration time.Duration, err error)

// Store is the persistence interface used by the API layer.
type Store interface {
	ListMatches(ctx context.Context) ([]Match, error)
	GetMatch(ctx context.Context, id int) (Match, error)
	GetScore(ctx context.Context, id int) (Score, error)
	UpdateScore(ctx context.Context, id int, s Score) error
	Leaderboard(ctx context.Context) ([]Standing, error)
	Close() error
}

// New opens the appropriate store. If databaseURL is empty, an in-memory
// dev store seeded with sample data is returned.
func New(ctx context.Context, databaseURL string, maxConns int, onQuery QueryFunc) (Store, error) {
	if databaseURL == "" {
		return NewMemoryStore(onQuery), nil
	}
	return NewPostgresStore(ctx, databaseURL, maxConns, onQuery)
}

// ------------------------- PostgreSQL store -------------------------

type PostgresStore struct {
	db       *sql.DB
	onQuery  QueryFunc
}

func NewPostgresStore(ctx context.Context, url string, maxConns int, onQuery QueryFunc) (*PostgresStore, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns / 2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{db: db, onQuery: onQuery}, nil
}

func (p *PostgresStore) timed(ctx context.Context, q string, fn func() error) error {
	start := time.Now()
	err := fn()
	if p.onQuery != nil {
		p.onQuery(q, time.Since(start), err)
	}
	return err
}

func (p *PostgresStore) ListMatches(ctx context.Context) ([]Match, error) {
	var out []Match
	err := p.timed(ctx, "SELECT matches", func() error {
		rows, err := p.db.QueryContext(ctx, `SELECT id, team1, team2, venue, match_date, status FROM matches ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Match
			if err := rows.Scan(&m.ID, &m.Team1, &m.Team2, &m.Venue, &m.MatchDate, &m.Status); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PostgresStore) GetMatch(ctx context.Context, id int) (Match, error) {
	var m Match
	err := p.timed(ctx, "SELECT match", func() error {
		return p.db.QueryRowContext(ctx,
			`SELECT id, team1, team2, venue, match_date, status FROM matches WHERE id=$1`, id).
			Scan(&m.ID, &m.Team1, &m.Team2, &m.Venue, &m.MatchDate, &m.Status)
	})
	if err == sql.ErrNoRows {
		return m, ErrNotFound
	}
	return m, err
}

func (p *PostgresStore) GetScore(ctx context.Context, id int) (Score, error) {
	var s Score
	var updated time.Time
	err := p.timed(ctx, "SELECT score", func() error {
		return p.db.QueryRowContext(ctx,
			`SELECT match_id, team1, team2, runs, wickets, overs, status, last_updated
			 FROM scores WHERE match_id=$1`, id).
			Scan(&s.MatchID, &s.Team1, &s.Team2, &s.Runs, &s.Wickets, &s.Overs, &s.Status, &updated)
	})
	s.LastUpdated = updated
	if err == sql.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

func (p *PostgresStore) UpdateScore(ctx context.Context, id int, s Score) error {
	return p.timed(ctx, "UPDATE score", func() error {
		_, err := p.db.ExecContext(ctx,
			`UPDATE scores SET team1=$2, team2=$3, runs=$4, wickets=$5, overs=$6,
			 status=$7, last_updated=$8 WHERE match_id=$1`,
			id, s.Team1, s.Team2, s.Runs, s.Wickets, s.Overs, s.Status, s.LastUpdated)
		return err
	})
}

func (p *PostgresStore) Leaderboard(ctx context.Context) ([]Standing, error) {
	var out []Standing
	err := p.timed(ctx, "SELECT standings", func() error {
		rows, err := p.db.QueryContext(ctx,
			`SELECT team, played, won, lost, nrr, points FROM standings ORDER BY points DESC, nrr DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st Standing
			if err := rows.Scan(&st.Team, &st.Played, &st.Won, &st.Lost, &st.NRR, &st.Points); err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PostgresStore) Close() error { return p.db.Close() }

// ------------------------- In-memory dev store -------------------------

// MemoryStore mirrors the Postgres schema in RAM. Intended for local
// development and unit tests where PostgreSQL is unavailable.
type MemoryStore struct {
	mu       sync.RWMutex
	matches  map[int]Match
	scores   map[int]Score
	standings []Standing
	onQuery  QueryFunc
	queries  int64
}

func NewMemoryStore(onQuery QueryFunc) *MemoryStore {
	m := &MemoryStore{
		matches: map[int]Match{
			104: {ID: 104, Team1: "India", Team2: "Australia", Venue: "Eden Gardens, Kolkata", MatchDate: "2026-04-18", Status: "live"},
			105: {ID: 105, Team1: "Mumbai Indians", Team2: "Chennai Super Kings", Venue: "Wankhede, Mumbai", MatchDate: "2026-04-19", Status: "live"},
			106: {ID: 106, Team1: "Royal Challengers Bengaluru", Team2: "Delhi Capitals", Venue: "M. Chinnaswamy, Bengaluru", MatchDate: "2026-04-20", Status: "completed"},
		},
		scores: map[int]Score{
			104: {MatchID: 104, Team1: "India", Team2: "Australia", Runs: 142, Wickets: 2, Overs: "15.4", Status: "live", LastUpdated: time.Now()},
			105: {MatchID: 105, Team1: "Mumbai Indians", Team2: "Chennai Super Kings", Runs: 186, Wickets: 5, Overs: "20.0", Status: "completed", LastUpdated: time.Now()},
			106: {MatchID: 106, Team1: "Royal Challengers Bengaluru", Team2: "Delhi Capitals", Runs: 198, Wickets: 4, Overs: "20.0", Status: "completed", LastUpdated: time.Now()},
		},
		standings: []Standing{
			{Team: "Kolkata Knight Riders", Played: 12, Won: 9, Lost: 3, NRR: 0.842, Points: 18},
			{Team: "Mumbai Indians", Played: 12, Won: 8, Lost: 4, NRR: 0.611, Points: 16},
			{Team: "Chennai Super Kings", Played: 12, Won: 7, Lost: 5, NRR: 0.433, Points: 14},
			{Team: "Royal Challengers Bengaluru", Played: 12, Won: 7, Lost: 5, NRR: 0.205, Points: 14},
			{Team: "Delhi Capitals", Played: 12, Won: 6, Lost: 6, NRR: -0.118, Points: 12},
			{Team: "Punjab Kings", Played: 12, Won: 4, Lost: 8, NRR: -0.502, Points: 8},
		},
		onQuery: onQuery,
	}
	return m
}

func (m *MemoryStore) timed(q string, fn func() error) error {
	start := time.Now()
	err := fn()
	if m.onQuery != nil {
		m.onQuery(q, time.Since(start), err)
	}
	return err
}

func (m *MemoryStore) ListMatches(ctx context.Context) ([]Match, error) {
	var out []Match
	err := m.timed("SELECT matches", func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()
		for _, v := range m.matches {
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

func (m *MemoryStore) GetMatch(ctx context.Context, id int) (Match, error) {
	var out Match
	err := m.timed("SELECT match", func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()
		v, ok := m.matches[id]
		if !ok {
			return ErrNotFound
		}
		out = v
		return nil
	})
	return out, err
}

func (m *MemoryStore) GetScore(ctx context.Context, id int) (Score, error) {
	var out Score
	err := m.timed("SELECT score", func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()
		v, ok := m.scores[id]
		if !ok {
			return ErrNotFound
		}
		out = v
		return nil
	})
	return out, err
}

func (m *MemoryStore) UpdateScore(ctx context.Context, id int, s Score) error {
	return m.timed("UPDATE score", func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.scores[id]; !ok {
			return ErrNotFound
		}
		s.LastUpdated = time.Now()
		m.scores[id] = s
		return nil
	})
}

func (m *MemoryStore) Leaderboard(ctx context.Context) ([]Standing, error) {
	var out []Standing
	err := m.timed("SELECT standings", func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()
		out = append(out, m.standings...)
		return nil
	})
	return out, err
}

func (m *MemoryStore) Close() error { return nil }

// MarshalScore serializes a Score for cache storage.
func MarshalScore(s Score) (json.RawMessage, error) {
	return json.Marshal(s)
}

var ErrNotFound = fmt.Errorf("not found")
