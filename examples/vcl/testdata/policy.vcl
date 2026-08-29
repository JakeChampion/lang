vcl 4.1;

import std;

backend origin {
    .host = "127.0.0.1";
    .port = "8080";
}

acl purgers {
    "localhost";
    "192.0.2.0"/24;
    ! "192.0.2.23";
}

sub vcl_recv {
    if (req.method == "PURGE") {
        if (!client.ip ~ purgers) {
            return (synth(405, "Not allowed"));
        }
        return (purge);
    }
    if (req.url ~ "^/static/") {
        unset req.http.Cookie;
        return (hash);
    }
    if (req.url ~ "^/admin") {
        std.log("admin bypass");
        return (pass);
    }
    return (hash);
}

sub vcl_hash {
    hash_data(req.url);
    hash_data(req.http.Host);
}

sub vcl_backend_response {
    if (beresp.status >= 500) {
        set beresp.uncacheable = true;
        set beresp.ttl = 0s;
        return (deliver);
    }
    set beresp.ttl = 1h;
    set beresp.http.X-Cached-By = "fern-vcl";
    return (deliver);
}

sub vcl_deliver {
    set resp.http.X-Hits = obj.hits;
    unset resp.http.X-Backend;
    return (deliver);
}
