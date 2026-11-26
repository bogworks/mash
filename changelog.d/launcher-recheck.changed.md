**The plugin re-checks its downloaded mash once per session, and `mash doctor` says which mash runs.** When the plugin
starts a session it now confirms its cached copy of mash still matches the release it pins, and replaces a copy that
does not (the old file is kept beside it). In a session the plugin started, `mash doctor` shows a `launcher` row: `ok`
for the pinned mash, and a warning when the plugin fell back to the mash on your PATH instead
([issue 1290](https://github.com/bogworks/mash/issues/1290)).
