-- +goose Up

-- Seed initial status thresholds
INSERT OR IGNORE INTO status_thresholds (status, days) VALUES
  ('applied', 14),
  ('screening', 7),
  ('interviewing', 7),
  ('offer', 3),
  ('accepted', 0),
  ('rejected', 0),
  ('withdrawn', 0),
  ('ghosted', 0);