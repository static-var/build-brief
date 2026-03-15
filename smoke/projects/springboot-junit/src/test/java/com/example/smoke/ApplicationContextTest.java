package com.example.smoke;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;

@SpringBootTest
class ApplicationContextTest {
    @Autowired
    GreetingService greetingService;

    @Test
    void contextLoads() {
        assertThat(greetingService.greeting("boot")).isEqualTo("hello, boot");
    }
}
