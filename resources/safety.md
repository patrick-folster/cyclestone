# Workspace Safety & Boundary Protection Rules

When executing tasks, always adhere to the following rules to protect workspace integrity in multi-workspace/multi-project environments:

- **Verify Working Directory**: Before writing, reading, modifying files, or executing terminal commands, verify that the current working directory (`Cwd`) and target file paths reside strictly within the intended repository root (`.`).
- **Do Not Write to Adjacent Repositories**: Do not create or write files, templates, or reports under adjacent or parent project directories (e.g. outside the root `.`).
- **Branch Naming**: Ensure that any git branches created or checked out are for the correct repository and follow the prescribed naming scheme for this milestone.
- **Graceful Error Handling**: If paths or directories expected by the milestone do not exist in the active workspace root (`.`), do not guess or write them to arbitrary locations on disk. Check your location and report the issue.
