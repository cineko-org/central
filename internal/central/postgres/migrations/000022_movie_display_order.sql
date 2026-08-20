ALTER TABLE movies
    ADD COLUMN display_order integer NOT NULL DEFAULT 2147483647;

ALTER TABLE movies
    ADD CONSTRAINT movies_display_order_nonnegative CHECK (display_order >= 0);

CREATE INDEX movies_display_order_idx
    ON movies (display_order, title, id)
    WHERE active;
