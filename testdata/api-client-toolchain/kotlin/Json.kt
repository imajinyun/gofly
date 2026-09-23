package kotlinx.serialization.json

class Json(configure: Builder.() -> Unit = {}) {
  class Builder {
    var ignoreUnknownKeys: Boolean = false
  }

  init {
    Builder().configure()
  }

  inline fun <reified T> decodeFromString(value: String): T {
    error("compile-only fixture")
  }
}
