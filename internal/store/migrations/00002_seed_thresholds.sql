-- +goose Up

-- Seed initial status thresholds.
-- Terminal statuses (accepted, rejected, withdrawn, ghosted) deliberately have
-- no row: staleness is derived from the threshold join, so a missing row means
-- "never stale". Seeding them would also violate the days > 0 CHECK.
INSERT OR IGNORE INTO status_thresholds (status, days) VALUES
  ('applied', 14),
  ('screening', 7),
  ('interviewing', 7),
  ('offer', 3);
