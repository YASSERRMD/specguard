# Contributing to Specguard

To maintain a clean and reliable codebase, all contributors must strictly adhere to the following commit and branching disciplines.

## Branching Strategy

1. Work must always be performed on a branch named for the active phase (for example: `phase_01`).
2. Do not commit or push directly to the `main` branch.
3. Once a phase is complete, push the branch, open a Pull Request, verify the CI results, and merge it. The branch should then be deleted.

## Commit Guidelines

1. **Commit Author**: All commits must be authored using the name `YASSERRMD` and email `arafath.yasser@gmail.com`.
2. **Atomic Commits**: Commit messages and changes should be scoped to small, independent, and logical units of work.
3. **No Em Dashes**: Never use em dashes (the Unicode character `\u2014`) in commit messages, comments, code, or documentation. Use simple hyphens instead.
4. **Writing Clean Code**: Before committing, make sure the codebase compiles, formatting is correct (`make lint`), and all unit tests pass (`make test`).
