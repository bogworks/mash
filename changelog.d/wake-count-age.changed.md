`mash serve` now wakes the session for a backlog, whatever the kinds of the waiting messages: when more than 15 are
waiting or the oldest has waited longer than 1 hour, at most once every 10 minutes and not in the first 10 minutes after
it starts. Change the limits with `MASH_WAKE_COUNT` / `wake_count` and `MASH_WAKE_AGE` / `wake_age`, or start
`mash serve --wake-count N --wake-age DURATION`; `0` turns a limit off. `mash inbox` already says on its second line when
a limit is passed.
