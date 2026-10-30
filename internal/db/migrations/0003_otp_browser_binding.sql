-- Login codes are bound to the browser that requested them: the browser
-- holds a random nonce in a cookie, the database stores its hash. Codes
-- issued before this migration have no nonce and can no longer be used.
ALTER TABLE otp_codes ADD COLUMN nonce_hash TEXT NOT NULL DEFAULT '';
UPDATE otp_codes SET used_at = created_at WHERE used_at IS NULL;
CREATE INDEX otp_codes_nonce ON otp_codes (email, nonce_hash, created_at);
