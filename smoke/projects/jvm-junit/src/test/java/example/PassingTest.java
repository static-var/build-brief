package example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class PassingTest {
    @Test
    void greetingReturnsExpectedValue() {
        assertEquals("hello, build-brief", App.greeting("build-brief"));
    }
}
