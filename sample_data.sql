-- Sample data for TRixie
-- Run this after the application creates the initial schema

-- Insert sample owners
INSERT INTO owners (name, email, phone, phone_display_options) VALUES
    ('John Doe', 'john.doe@example.com', '+1234567890', 'call,sms,whatsapp'),
    ('Jane Smith', 'jane.smith@example.com', '+9876543210', 'call,whatsapp'),
    ('Alice Johnson', 'alice.j@example.com', '+1122334455', 'call,sms');

-- Insert sample items
INSERT INTO items (item_code, description) VALUES
    ('KEY-001', 'House keys with red keychain'),
    ('BAG-2024', 'Blue backpack'),
    ('LAPTOP-001', 'MacBook Pro 14-inch'),
    ('WALLET-JD', 'Brown leather wallet'),
    ('PHONE-CASE', 'Black phone case with cards');

-- Link items to owners (many-to-many relationships)
-- John's items
INSERT INTO item_owners (item_id, owner_id) VALUES
    (1, 1),  -- KEY-001 belongs to John
    (4, 1);  -- WALLET-JD belongs to John

-- Jane's items
INSERT INTO item_owners (item_id, owner_id) VALUES
    (2, 2),  -- BAG-2024 belongs to Jane
    (5, 2);  -- PHONE-CASE belongs to Jane

-- Alice's items
INSERT INTO item_owners (item_id, owner_id) VALUES
    (3, 3);  -- LAPTOP-001 belongs to Alice

-- Shared item (laptop belongs to both Jane and Alice)
INSERT INTO item_owners (item_id, owner_id) VALUES
    (3, 2);  -- LAPTOP-001 also belongs to Jane (shared laptop)

-- To load this data:
-- 1. Start the application to create the database
-- 2. Stop the application
-- 3. Run: sqlite3 qr_tracker.db < sample_data.sql
-- 4. Restart the application
