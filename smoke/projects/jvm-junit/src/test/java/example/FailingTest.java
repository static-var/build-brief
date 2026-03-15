package example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class FailingTest {
    @Test
    void intentionalFailure() {
        assertEquals("expected", App.greeting("build-brief"));
    }
}
