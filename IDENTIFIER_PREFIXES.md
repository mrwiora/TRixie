# Item Identifier Prefixes

## Overview

The item identifier prefix feature allows you to define reusable prefixes (like "ITEM", "KEY", "BAG") that automatically generate sequential item codes. Instead of manually typing "ITEM-1", "ITEM-2", "ITEM-3", etc., the system will auto-increment the counter for you.

## How It Works

### 1. Create Identifier Prefixes

On the admin panel, you'll find the "Item Identifier Prefixes" section where you can:
- Add new prefixes (e.g., "ITEM", "KEY", "BAG", "LAPTOP")
- View existing prefixes and their current counters
- See what the next generated code will be

**Example:**
- Prefix: `ITEM`
- Current Counter: 5
- Next Code: `ITEM-5`

### 2. Create Items Using Prefixes

When adding a new item, you have two options:

#### Option A: Auto-generate from prefix
1. Select "Auto-generate from prefix"
2. Choose a prefix from the dropdown
3. The system will automatically generate the item code (e.g., `ITEM-5`)
4. The counter increments automatically for next time

#### Option B: Enter manually
1. Select "Enter manually"
2. Type in any custom item code you want
3. This doesn't affect any prefix counters

### 3. Editing Items

When editing an item:
- You can manually change the item code to anything you want
- Editing an item code that was auto-generated **does not** affect the prefix counter
- The prefix counter only increments when creating new items using that prefix

## Database Schema

The system adds a new table `identifier_prefixes`:

```sql
CREATE TABLE identifier_prefixes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    prefix TEXT UNIQUE NOT NULL,
    current_counter INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Use Cases

### Scenario 1: Office Equipment Tracking
- Create prefix `LAPTOP` for laptops (LAPTOP-1, LAPTOP-2, ...)
- Create prefix `MONITOR` for monitors (MONITOR-1, MONITOR-2, ...)
- Create prefix `MOUSE` for mice (MOUSE-1, MOUSE-2, ...)

### Scenario 2: Key Management
- Create prefix `KEY` for all keys (KEY-1, KEY-2, ...)
- Create prefix `FOBKEY` for key fobs (FOBKEY-1, FOBKEY-2, ...)

### Scenario 3: Mixed System
- Use prefixes for standard items (ITEM-1, ITEM-2, ...)
- Use manual codes for special items (VIP-SPECIAL, LEGACY-001, ...)

## Features

✅ **Atomic Counter Increment** - Uses database transactions to prevent duplicate codes  
✅ **Flexible** - Choose between auto-generated or manual codes for each item  
✅ **Simple Format** - Always generates codes as `PREFIX-NUMBER`  
✅ **Edit Freedom** - Edit item codes after creation without affecting counters  
✅ **No Conflicts** - Each prefix has its own independent counter  

## Technical Notes

- Counters start at 1 by default
- The format is always `{PREFIX}-{NUMBER}` (e.g., ITEM-1, KEY-42)
- Prefixes must be unique
- When generating a code, the system uses a database transaction to ensure no duplicates
- Deleting a prefix does not affect existing items using that prefix format
- The counter only increments when successfully creating an item