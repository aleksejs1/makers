-- Extra project links (social accounts, app stores, …), one URL per line.
ALTER TABLE projects ADD COLUMN links TEXT NOT NULL DEFAULT '';
