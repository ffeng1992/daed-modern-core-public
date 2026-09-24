# Site DNS compatibility transport optimization

This opt-in tool changes the reviewed compatibility adapter, not daed, DAE,
frontend routing, filtering, or the database. It adds a bounded pool of 16
persistent loopback TCP connections to the core. Each connection has at most
one outstanding query. Requests include a six-second deadline covering queue
wait; invalid responses and timeouts are not retried. EOF/reset may retry once.
There is no added answer cache or positive-answer path around the core.

The adapter's arbitrary close after 32 TCP queries is removed. Existing idle,
frame-size, UDP concurrency and systemd memory limits stay unchanged. One TCP
client is still served sequentially; this is not a claim to remove every
head-of-line wait or improve every cold upstream lookup.

`build_adapter.py SOURCE DESTINATION` requires the exact reviewed source hash.
It refuses unknown site revisions. The input/output retain private settings
and must remain outside the repository. Copy `dns_transport.py` alongside the
result. Review, shadow-test, isolate, back up and arm a bounded rollback before
any service replacement. Never overwrite a live adapter solely because the
builder succeeded. A production helper restart is required for activation;
this does not require a daemon/core restart. A running service process is not
protocol readiness: require consecutive successful UDP and TCP queries through
both the helper and frontend before starting acceptance. The helper restart
can briefly interrupt existing requests; no zero-downtime update is claimed.

Run `python3 -m unittest discover -s tools/dns-compat -v` from the repository.
Transport tests use synthetic loopback servers. The pipeline and gate tests
also require `SITE_ADAPTER_PATH` pointing to the generated private adapter;
without it they are explicitly skipped. Site acceptance must run those tests
and real answer semantics separately. The private source is not a CI secret
or artifact.

Reuse lowers connection churn; it does not guarantee arbitrary future upstream
compatibility, unlimited throughput, zero errors, or uninterrupted reloads.
