Create a new database migration for: $ARGUMENTS

Follow this process:
1. Read the root CLAUDE.md and migrations/CLAUDE.md
2. Determine the next migration number from existing files
3. Create both .up.sql and .down.sql files
4. Write the migration SQL
5. Run `make migrate-up` to apply
6. Run `make migrate-down` to verify reversibility
7. Run `make migrate-up` again to confirm clean re-application
