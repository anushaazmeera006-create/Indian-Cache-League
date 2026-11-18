-- ICL database schema: matches, live scores, leaderboard standings.

CREATE TABLE IF NOT EXISTS matches (
    id          INTEGER PRIMARY KEY,
    team1       TEXT NOT NULL,
    team2       TEXT NOT NULL,
    venue       TEXT NOT NULL,
    match_date  TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'scheduled'
);

CREATE TABLE IF NOT EXISTS scores (
    match_id      INTEGER PRIMARY KEY REFERENCES matches(id),
    team1         TEXT NOT NULL,
    team2         TEXT NOT NULL,
    runs          INTEGER NOT NULL DEFAULT 0,
    wickets       INTEGER NOT NULL DEFAULT 0,
    overs         TEXT NOT NULL DEFAULT '0.0',
    status        TEXT NOT NULL DEFAULT 'live',
    last_updated  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS standings (
    team    TEXT PRIMARY KEY,
    played  INTEGER NOT NULL DEFAULT 0,
    won     INTEGER NOT NULL DEFAULT 0,
    lost    INTEGER NOT NULL DEFAULT 0,
    nrr     DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    points  INTEGER NOT NULL DEFAULT 0
);

INSERT INTO matches (id, team1, team2, venue, match_date, status) VALUES
    (104, 'India', 'Australia', 'Eden Gardens, Kolkata', '2026-04-18', 'live'),
    (105, 'Mumbai Indians', 'Chennai Super Kings', 'Wankhede, Mumbai', '2026-04-19', 'live'),
    (106, 'Royal Challengers Bengaluru', 'Delhi Capitals', 'M. Chinnaswamy, Bengaluru', '2026-04-20', 'completed')
ON CONFLICT (id) DO NOTHING;

INSERT INTO scores (match_id, team1, team2, runs, wickets, overs, status) VALUES
    (104, 'India', 'Australia', 142, 2, '15.4', 'live'),
    (105, 'Mumbai Indians', 'Chennai Super Kings', 186, 5, '20.0', 'completed'),
    (106, 'Royal Challengers Bengaluru', 'Delhi Capitals', 198, 4, '20.0', 'completed')
ON CONFLICT (match_id) DO NOTHING;

INSERT INTO standings (team, played, won, lost, nrr, points) VALUES
    ('Kolkata Knight Riders', 12, 9, 3, 0.842, 18),
    ('Mumbai Indians', 12, 8, 4, 0.611, 16),
    ('Chennai Super Kings', 12, 7, 5, 0.433, 14),
    ('Royal Challengers Bengaluru', 12, 7, 5, 0.205, 14),
    ('Delhi Capitals', 12, 6, 6, -0.118, 12),
    ('Punjab Kings', 12, 4, 8, -0.502, 8)
ON CONFLICT (team) DO NOTHING;
