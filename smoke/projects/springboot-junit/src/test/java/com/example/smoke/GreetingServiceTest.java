package com.example.smoke;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;

class GreetingServiceTest {
    private final GreetingService service = new GreetingService();

    @Test
    void greetingUsesProvidedName() {
        assertThat(service.greeting("spring")).isEqualTo("hello, spring");
    }
}
