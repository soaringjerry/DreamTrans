-- 课表归类: a course can declare its weekly class times. Sessions are then
-- matched to courses by time overlap (no model involved): at creation from
-- the start time, at completion from the recorded span, and on demand over
-- the whole history. A link made by the timetable is marked so a later run
-- may move it, while a link a person made by hand is never touched.

CREATE TABLE IF NOT EXISTS course_slots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  -- ISO weekday: 1 = Monday … 7 = Sunday.
  weekday SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7),
  -- Wall-clock minutes since local midnight in `timezone`; a slot never
  -- crosses midnight.
  start_minute SMALLINT NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
  end_minute SMALLINT NOT NULL CHECK (end_minute > start_minute AND end_minute <= 1440),
  timezone VARCHAR(64) NOT NULL CHECK (length(timezone) > 0),
  label VARCHAR(60) NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_course_slots_project ON course_slots (project_id, weekday, start_minute);

ALTER TABLE project_sessions
  ADD COLUMN IF NOT EXISTS assigned_by VARCHAR(10) NOT NULL DEFAULT 'manual'
    CHECK (assigned_by IN ('manual', 'timetable')),
  ADD COLUMN IF NOT EXISTS slot_id UUID REFERENCES course_slots(id) ON DELETE SET NULL;
