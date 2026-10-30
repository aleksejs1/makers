-- A login code becomes usable only once its email was handed to the mail
-- server; until then it must not shadow an older, delivered code.
ALTER TABLE otp_codes ADD COLUMN sent_at TEXT;
UPDATE otp_codes SET sent_at = created_at;
