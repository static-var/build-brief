package example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

class ParameterizedGreetingTest {
    @ParameterizedTest
    @CsvSource(
        value = {
            "build-brief|hello, build-brief",
            "world|hello, world",
            "Gradle|hello, Gradle"
        },
        delimiter = '|'
    )
    void greetingReturnsExpectedValueForMultipleInputs(String name, String expected) {
        assertEquals(expected, App.greeting(name));
    }
}
