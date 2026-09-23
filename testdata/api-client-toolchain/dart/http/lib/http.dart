library http;

class Response {
  Response(this.body, this.statusCode);

  final String body;
  final int statusCode;
}

class Client {
  Future<Response> get(Uri uri, {Map<String, String>? headers}) async => Response('{}', 200);

  Future<Response> post(Uri uri, {Map<String, String>? headers, Object? body}) async => Response('{}', 200);

  Future<Response> delete(Uri uri, {Map<String, String>? headers, Object? body}) async => Response('', 204);

  void close() {}
}
