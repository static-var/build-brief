package example;

import org.junit.jupiter.api.Test;

class ExceptionTest {
    @Test
    void throwsHelpfulException() {
        throw new IllegalStateException("kaboom from ExceptionTest");
    }
}
