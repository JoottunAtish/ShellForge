# Uninstall

> **Status: outline.** Written on Day 7.

Be complete and be explicit. Orphaned 2 GB disk images are where the angry issues
come from, and this page existing properly is the difference.

## Keep your progress first, if you want it

```
shellforge export --format=json > shellforge-progress.json
```

Uninstalling removes your progress permanently. There is no cloud copy, because
there is no cloud.

## Remove everything

```
shellforge sandbox destroy      # removes the WSL distribution or the container
shellforge uninstall            # removes config, progress, and cache
```

Then remove the binary itself. Exact paths per platform, to be filled in.

## Verify it is actually gone

This section is the entire point of the page. Do not skip it.

**Windows:**

```powershell
wsl -l -v
```

`shellforge-sandbox` must not be listed.

Then open File Explorer and confirm the install directory is gone. It held a
`.vhdx` file of roughly 2 GB, and if `wsl --unregister` did not complete, that file
is still on your disk taking up space with nothing pointing at it. The exact path
goes here.

**Linux:**

```bash
docker images | grep shellforge
docker ps -a | grep shellforge
```

Both should print nothing.

## Manual cleanup, if something went wrong

The exact commands to unregister the distribution by hand, delete the directory,
remove the container image, and a complete list of every path Shellforge could have
written to, per operating system.

## What is left behind after all of this

Nothing. That is the claim this page has to be able to make honestly, so it gets
verified on a real machine before release, with File Explorer open.
