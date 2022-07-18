ALTER TABLE reservations ADD price INTEGER UNSIGNED NOT NULL;

UPDATE 
reservations r 
INNER JOIN sheets s
ON s.id = r.sheet_id
INNER JOIN events e
ON e.id = r.event_id
SET r.price = e.price + s.price;

ALTER TABLE events ADD s_remains INTEGER UNSIGNED NOT NULL;
ALTER TABLE events ADD a_remains INTEGER UNSIGNED NOT NULL;
ALTER TABLE events ADD b_remains INTEGER UNSIGNED NOT NULL;
ALTER TABLE events ADD c_remains INTEGER UNSIGNED NOT NULL;
