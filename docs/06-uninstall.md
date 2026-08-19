# Uninstall

> **Status: partial.** `shellforge sandbox destroy` is real as of issue #71 and
> this page's sandbox section describes it. `shellforge export` and
> `shellforge uninstall`, named below, are not built yet: neither verb is
> registered today, and running either one prints cobra's own unknown command
> error. That gap is tracked as issue #139, not hidden. Do not read this page
> as a complete uninstall path until both verbs land.

Be complete and be explicit. Orphaned 2 GB disk images are where the angry issues
come from, and this page existing properly is the difference.

## `shellforge sandbox destroy` removes the sandbox only

```
shellforge sandbox destroy
```

This removes the sandbox and nothing else:

- On Windows, the `shellforge-sandbox` WSL distribution, and its install
  directory, including the backing `.vhdx` file of roughly 2 GB.
- On Linux and macOS, the `shellforge-sandbox` Docker container and the
  `shellforge-sandbox` image.

It asks you to type the sandbox name to confirm, unless you pass `--yes`. Before
it asks, it prints exactly what it is about to remove, including the absolute
directory it will delete on Windows. After it removes the sandbox, it checks
again rather than trusting a successful removal call, and prints the same
absolute path a second time so you always have it to check by hand.

**Your progress, config, and cache are a separate step.** `sandbox destroy`
does not touch the progress database, and it does not touch anything under
your Shellforge data directory other than the sandbox files named above.
Keeping or removing that data is not this command's job.

## Keep your progress first, if you want it

```
shellforge export --format=json > shellforge-progress.json
```

**Not built yet.** `export` is not a registered verb today. Once it exists,
uninstalling the rest of Shellforge will remove your progress permanently, and
there is no cloud copy to fall back on, because there is no cloud.

## Remove everything

```
shellforge sandbox destroy      # real: removes the WSL distribution or the container
shellforge uninstall            # not built yet: removes config, progress, and cache
```

Then remove the binary itself. Exact paths per platform, to be filled in.

## Verify it is actually gone

This section is the entire point of the page. Do not skip it.

**Windows:**

```powershell
wsl -l -v
```

`shellforge-sandbox` must not be listed. `shellforge sandbox destroy` itself
runs this same check for you and fails loudly, naming the absolute path to
check in Explorer, if the distribution is still there afterward: it never
reports success on a removal it has not confirmed.

Then open File Explorer and confirm the install directory `sandbox destroy`
printed is gone. It held a `.vhdx` file of roughly 2 GB, and if the underlying
`wsl --unregister` did not complete, that file is still on your disk taking up
space with nothing pointing at it.

**Linux:**

```bash
docker images | grep shellforge
docker ps -a | grep shellforge
```

Both should print nothing. `shellforge sandbox destroy` runs the equivalent
check itself before it reports success.

## Manual cleanup, if something went wrong

The exact commands to unregister the distribution by hand, delete the directory,
remove the container image, and a complete list of every path Shellforge could have
written to, per operating system.

## What is left behind after all of this

Your progress database, config, and cache: `sandbox destroy` deliberately does
not touch them, per the section above. Once `export` and `uninstall` exist,
this section states plainly what running both leaves behind, which should be
nothing, and that claim gets verified on a real machine before release, with
File Explorer open.
