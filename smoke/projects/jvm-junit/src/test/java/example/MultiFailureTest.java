package example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class MultiFailureTest {
    @Test
    void wrongGreeting() {
        assertEquals("expected", App.greeting("build-brief"));
    }

    @Test
    void unexpectedException() {
        throw new IllegalArgumentException("bad input from MultiFailureTest");
    }
}
