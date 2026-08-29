vcl 4.1;

import std;
import directors;

include "acls.vcl";

probe healthcheck {
    .url = "/healthz";
    .timeout = 1s;
    .interval = 5s;
    .window = 5;
    .threshold = 3;
}

backend origin {
    .host = "127.0.0.1";
    .port = "8080";
    .connect_timeout = 1s;
    .first_byte_timeout = 5s;
    .max_connections = 200;
    .probe = healthcheck;
}

backend spare {
    .host = "127.0.0.2";
    .port = "8080";
    .probe = {
        .url = "/";
        .interval = 10s;
    }
}

backend broken none;

acl purgers {
    "localhost";
    "192.0.2.0"/24;
    ! "192.0.2.23";
    ( "www.example.com" );
}

sub vcl_init {
    new rr = directors.round_robin();
    rr.add_backend(origin);
    rr.add_backend(spare);
}

sub vcl_recv {
    set req.backend_hint = rr.backend();

    if (req.method == "PURGE") {
        if (!client.ip ~ purgers) {
            return (synth(405, "Not allowed"));
        }
        return (purge);
    }

    if (req.http.X-Forwarded-Proto !~ "^https$") {
        set req.http.X-Forwarded-Proto = "https";
    } elseif (req.url ~ "^/admin") {
        std.log("admin hit");
        call mark_private;
    } elsif (req.url ~ "^/static/") {
        unset req.http.Cookie;
    } else if (req.url == "/") {
        set req.http.X-Home = "1";
    } else {
        set req.http.X-Other = {"a literal " with quotes"};
    }

    if (req.method != "GET" && req.method != "HEAD") {
        return (pass);
    }

    return (hash);
}

sub mark_private {
    set req.http.X-Private = "yes";
}

sub vcl_backend_response {
    if (beresp.status >= 500 || beresp.status == 0) {
        return (retry);
    }
    set beresp.ttl = 1h;
    set beresp.grace = 24h;
    set beresp.http.X-Size = "" + 256KB;
    return (deliver);
}

sub vcl_deliver {
    unset resp.http.X-Varnish;
    set resp.http.X-Cache = "HIT";
    return;
}
