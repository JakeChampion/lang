vcl 4.1;

backend origin {
    .host = "127.0.0.1";
    .port = "9000";
}

sub vcl_recv {
    if (req.http.X-Bypass) {
        return (pass);
    }
    if (req.url ~ "^/nocache") {
        return (pass);
    }
    return (hash);
}

sub vcl_hash {
    hash_data(req.url);
}

sub vcl_backend_response {
    set beresp.http.X-Proxied-By = "fern-vcl";
    return (deliver);
}

sub vcl_deliver {
    set resp.http.X-Cache-Hits = obj.hits;
    return (deliver);
}
