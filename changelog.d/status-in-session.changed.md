`status` and `doctor`, asked through the tools of a session, now report on that session itself: "ready, online" means
this session holds the endpoint and its mail will reach it. When another session holds it, status says "not ready:
another session holds this endpoint"; when a newer session took it, "this session lost this endpoint to a newer one";
when this session could not take it, "this session could not take this endpoint:" with the cause on the next line.
`doctor`'s delivery row says the same. At a shell nothing changes.
