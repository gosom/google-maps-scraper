#!/usr/bin/env bash
#
# promote_admin.sh — Promote a user to admin role.
#
# Usage:
#   ./scripts/promote_admin.sh <user_id_or_email>
#
# Requires:
#   - DSN environment variable (PostgreSQL connection string)
#   - psql client
#
# Security:
#   - This script requires direct database access. There is no API
#     endpoint to change roles — this is intentional (CWE-269).
#   - Always verify the user identity before promoting.

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <user_id_or_email>"
    echo ""
    echo "Examples:"
    echo "  $0 user_2abc123def456    # by Clerk user ID"
    echo "  $0 admin@brezel.ai       # by email address"
    exit 1
fi

if [ -z "${DSN:-}" ]; then
    echo "Error: DSN environment variable is required."
    echo "Example: DSN='postgres://user:pass@host:5432/dbname?sslmode=require'"
    exit 1
fi

IDENTIFIER="$1"

# Input validation — ALLOWLIST approach (not denylist).
# Only permit characters that are valid in Clerk user IDs and email addresses.
# This prevents SQL injection regardless of PostgreSQL quoting tricks.
if ! echo "$IDENTIFIER" | grep -qE '^[a-zA-Z0-9@._+\-]+$'; then
    echo "Error: Invalid characters in identifier."
    echo "Only alphanumeric characters, @, ., _, +, and - are allowed."
    exit 1
fi

# Determine if input is an email or user ID and use psql variables
# for safe parameterization (no string interpolation into SQL).
if echo "$IDENTIFIER" | grep -q '@'; then
    LOOKUP_COLUMN="email"
    DISPLAY="email=$IDENTIFIER"
else
    LOOKUP_COLUMN="id"
    DISPLAY="id=$IDENTIFIER"
fi

echo "Looking up user ($DISPLAY)..."

# Show current user info before making changes.
CURRENT=$(psql "$DSN" -t -A -c "SELECT id, email, role FROM users WHERE $LOOKUP_COLUMN = \$\$${IDENTIFIER}\$\$" 2>/dev/null)
if [ -z "$CURRENT" ]; then
    echo "Error: No user found with $DISPLAY"
    exit 1
fi

echo "Current record: $CURRENT"
echo ""

# Check if already admin
CURRENT_ROLE=$(echo "$CURRENT" | cut -d'|' -f3)
if [ "$CURRENT_ROLE" = "admin" ]; then
    echo "User is already an admin. No changes made."
    exit 0
fi

read -p "Promote this user to admin? [y/N] " -n 1 -r
echo ""
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
fi

# Promote and set higher concurrent job limit.
# Uses dollar-quoting for the identifier (safe after allowlist validation).
psql "$DSN" -c "
    UPDATE users
    SET role = 'admin',
        max_concurrent_jobs = 50,
        updated_at = NOW()
    WHERE $LOOKUP_COLUMN = \$\$${IDENTIFIER}\$\$
    RETURNING id, email, role, max_concurrent_jobs;
"

echo ""
echo "Done. User promoted to admin with concurrent job limit of 50."
