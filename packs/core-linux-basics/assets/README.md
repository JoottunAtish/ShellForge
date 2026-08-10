# Assets

Files materialized into the sandbox during level setup, referenced from a level's
`setup.files[].source`.

## Rules

- **Assets must be internally consistent with their level.** If the level says
  the answer is 147, the log file here must actually contain 147 matching lines.
  Verify by running the level's `solution`, not by adjusting the check.
- **Realistic and in-world.** These are Meridian Logistics files: shipping
  manifests, delivery logs, access logs, config for a service called `atlas`.
  The fiction costs nothing and makes the exercise feel like work rather than
  homework.
- **Deterministic.** A generated asset uses a fixed seed and is committed, or is
  declared with `setup.files[].generate` and produced at setup time from a seed.
  Never both.
- **LF line endings.** `.gitattributes` enforces it and the setup runner strips
  carriage returns again on materialization.
- **No secrets, even fake-looking ones.** Level find-02 deliberately plants a
  string that looks like a leaked API key. Keep it obviously synthetic so no
  scanner ever has a reason to flag this repository.

## Status

Empty. Assets arrive alongside the levels that use them.
