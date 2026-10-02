package kotlinx.serialization

import kotlinx.serialization.json.Json

@Target(AnnotationTarget.CLASS)
annotation class Serializable

fun <T> Json.encodeToString(value: T): String = "{}"
