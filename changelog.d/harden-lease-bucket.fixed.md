The lease bucket is hardened: it no longer allows rollup writes, `mash admin init` applies this to an existing bucket, and the admin no longer purges lease keys (it deletes them by revision).
