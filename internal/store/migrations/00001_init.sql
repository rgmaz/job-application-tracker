-- +goose Up

CREATE TABLE applications (
  id INTEGER PRIMARY KEY,
  company TEXT NOT NULL,
  role TEXT NOT NULL,
  location TEXT,
  job_url TEXT,
  source TEXT NOT NULL DEFAULT 'other',
  applied_on TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'applied'
    CHECK (status IN ('applied','screening','interviewing','offer',
                      'accepted','rejected','withdrawn','ghosted')),
  status_changed_on TEXT NOT NULL,
  status_changed_at INTEGER NOT NULL,
  follow_up_on TEXT,
  contact_name TEXT,
  contact_email TEXT,
  email_subject TEXT,
  email_link TEXT,
  notes TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE documents (
  id INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('job_ad','cv','cover_letter','email','other')),
  original_name TEXT NOT NULL,
  mime TEXT NOT NULL,
  size INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  storage_key TEXT NOT NULL,
  uploaded_at INTEGER NOT NULL
);

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK (type IN ('status_change','note','email_sent',
                                     'email_received','document_added')),
  body TEXT,
  occurred_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE status_thresholds (
  status TEXT PRIMARY KEY,
  days INTEGER NOT NULL CHECK (days > 0)
);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- Indexes for common query patterns
CREATE INDEX idx_applications_status ON applications(status);
CREATE INDEX idx_applications_status_changed ON applications(status_changed_on);
CREATE INDEX idx_applications_created ON applications(created_at);
CREATE INDEX idx_applications_applied_on ON applications(applied_on);
CREATE INDEX idx_documents_application_id ON documents(application_id);
CREATE INDEX idx_events_application_id ON events(application_id);

-- Goose: Down section is omitted because sqlc parses the same files as its
-- schema. Down migrations would drop tables that sqlc reads; rollback is
-- restore from the DATA_DIR backup.