package com.fasterxml.jackson.databind;

public final class ObjectMapper {
  public String writeValueAsString(Object value) {
    return "{}";
  }

  public <T> T readValue(String value, Class<T> type) {
    return null;
  }
}
